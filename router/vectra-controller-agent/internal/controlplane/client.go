package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	RouterID   string
	AgentToken string
	Timeout    time.Duration
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
		bodyPreview, readErr := io.ReadAll(io.LimitReader(response.Body, 2048))
		if readErr != nil {
			return fmt.Errorf(
				"unexpected status %d for %s (failed to read response body: %w)",
				response.StatusCode,
				path,
				readErr,
			)
		}

		trimmed := strings.TrimSpace(string(bodyPreview))
		if trimmed != "" {
			return fmt.Errorf(
				"unexpected status %d for %s: %s",
				response.StatusCode,
				path,
				trimmed,
			)
		}

		return fmt.Errorf("unexpected status %d for %s", response.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
