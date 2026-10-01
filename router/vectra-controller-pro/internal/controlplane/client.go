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
)

// Options configure a control-plane Client.
type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	RouterID   string
	AgentToken string
	Timeout    time.Duration
	// SocketMark is the SO_MARK stamped on this client's sockets. The
	// nftables output chain returns on it as its FIRST rule, keeping
	// controller->panel traffic out of the provider's routing. Ignored when
	// HTTPClient is supplied (tests) or off Linux. 0 disables.
	SocketMark int
}

// Client is a thin HTTPS client for the panel's router-facing API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	routerID   string
	agentToken string
}

// NewClient builds a Client. A nil HTTPClient gets a default with the timeout.
func NewClient(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout, Transport: markedTransport(opts.SocketMark), CheckRedirect: noRedirect}
	}
	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		httpClient: client,
		routerID:   opts.RouterID,
		agentToken: opts.AgentToken,
	}
}

// markedTransport returns an http.Transport whose dialer stamps SO_MARK on
// every connection. Returns nil (i.e. http.DefaultTransport) when unmarked.
func markedTransport(mark int) http.RoundTripper {
	control := setSocketMark(mark)
	if control == nil {
		return nil
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Names resolved on this same marked path (resolve.go), not by dnsmasq:
	// its upstream goes through the tunnel while vctl carries the router.
	t.DialContext = resolvingDial(d, directResolver(control))
	return t
}

// MarkedDialer dials with SO_MARK = mark on every socket (Linux; a plain
// dialer elsewhere): the control plane's own way past the TPROXY rules, which
// works with xray down and with the kill switch armed.
func MarkedDialer(mark int, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: setSocketMark(mark)}
}

// SetCredentials updates the router identity used for authenticated calls.
// HTTPClient exposes the client's transport so a caller can probe the panel
// over the SAME socket path the control plane uses.
//
// That distinction is load-bearing, not cosmetic. Once the vctl ruleset is
// loaded, the output chain's last rule stamps FwMark on any unmarked local
// tcp/udp egress, and the fwmark policy route resolves that to
// `local 0.0.0.0/0 dev lo` — so a probe built on a bare &http.Client{} never
// leaves the box, no matter how healthy the panel is. Only sockets carrying
// SO_MARK = firewall.DefaultControlMark get out, and this client's do.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

func (c *Client) SetCredentials(routerID string, agentToken string) {
	c.routerID = routerID
	c.agentToken = agentToken
}

// Register enrolls the router and (on success) returns an issued token.
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

// CheckIn reports inventory/health and returns jobs + desired config.
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

// SubmitJobResult reports the outcome of a delivered job.
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
			return fmt.Errorf("unexpected status %d for %s (failed to read response body: %w)",
				response.StatusCode, path, readErr)
		}
		trimmed := strings.TrimSpace(string(bodyPreview))
		if trimmed != "" {
			return fmt.Errorf("unexpected status %d for %s: %s", response.StatusCode, path, trimmed)
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

// noRedirect answers a redirect with the redirect itself: an answer from
// anywhere but the endpoint is no answer, and following one would carry the
// router's token (x-vectra-router-token) to wherever it points — another
// host, or plain http.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// MarkedHTTPClient is an HTTP client on the control plane's path: SO_MARK =
// mark on every socket, names resolved on the same path, and no redirect
// followed (noRedirect).
func MarkedHTTPClient(mark int, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
	if t := markedTransport(mark); t != nil {
		c.Transport = t
	}
	return c
}
