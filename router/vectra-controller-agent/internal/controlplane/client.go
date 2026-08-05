package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"vectra-controller-agent/internal/netmark"
)

// StatusError is returned when the control plane responds with a non-2xx
// status. It preserves the status code so callers can react to specific
// outcomes (e.g. a 403 register rejection triggers keypair-signed recovery)
// while keeping the original human-readable error string.
type StatusError struct {
	StatusCode int
	Path       string
	Body       string
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("unexpected status %d for %s: %s", e.StatusCode, e.Path, e.Body)
	}
	return fmt.Sprintf("unexpected status %d for %s", e.StatusCode, e.Path)
}

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	RouterID   string
	AgentToken string
	Timeout    time.Duration
	// Fwmark, when non-zero, stamps SO_MARK on the agent's sockets so the
	// control-plane traffic bypasses the PassWall2 tproxy and always egresses
	// directly. Zero leaves dialing unchanged. Linux-only (no-op elsewhere).
	Fwmark uint
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	routerID   string
	agentToken string
}

func NewClient(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
		if control := netmark.Control(opts.Fwmark); control != nil {
			dialer := &net.Dialer{Timeout: timeout, Control: control}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DialContext = dialer.DialContext
			client.Transport = transport
		}
	}
	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		httpClient: client,
		routerID:   opts.RouterID,
		agentToken: opts.AgentToken,
	}
}

func (c *Client) SetCredentials(routerID string, agentToken string) {
	c.routerID = routerID
	c.agentToken = agentToken
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (RegisterResponse, error) {
	if req.ProtocolVersion == "" {
		req.ProtocolVersion = ProtocolVersion
	}
	var out RegisterResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/router/register", req, &out); err != nil {
		return RegisterResponse{}, err
	}
	return out, nil
}

func (c *Client) CheckIn(ctx context.Context, req CheckInRequest) (CheckInResponse, error) {
	if req.ProtocolVersion == "" {
		req.ProtocolVersion = ProtocolVersion
	}
	var out CheckInResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/router/check-in", req, &out); err != nil {
		return CheckInResponse{}, err
	}
	return out, nil
}

func (c *Client) SubmitJobResult(ctx context.Context, req JobResultRequest) (JobResultResponse, error) {
	if req.ProtocolVersion == "" {
		req.ProtocolVersion = ProtocolVersion
	}
	var out JobResultResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/router/job-result", req, &out); err != nil {
		return JobResultResponse{}, err
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.routerID != "" {
		request.Header.Set("x-vectra-router-id", c.routerID)
	}
	if c.agentToken != "" {
		request.Header.Set("x-vectra-router-token", c.agentToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		bodyPreview, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return &StatusError{
			StatusCode: response.StatusCode,
			Path:       path,
			Body:       strings.TrimSpace(string(bodyPreview)),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
