package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StatusError is returned when the control plane answers a non-2xx status. It
// preserves the status code and the (trimmed, length-capped) response body so
// callers can react to a specific outcome — the A2 one-shot re-sign reads the
// 401 body to find the fresh nonce — while Error() keeps the original
// human-readable wording.
//
// Confidential marks a path whose response body must never be surfaced in the
// error string: the check-in answer can reflect confidential request fields
// (e.g. Wi-Fi passwords carried in the Connect telemetry), so its body is kept
// in the struct for the retry logic but omitted from Error(). This preserves
// vctl's long-standing behaviour of not echoing the check-in response body
// (TestConnectClientNeverEchoesConfidentialCheckinErrors).
type StatusError struct {
	StatusCode   int
	Path         string
	Body         string
	Confidential bool
}

func (e *StatusError) Error() string {
	if e.Body != "" && !e.Confidential {
		return fmt.Sprintf("unexpected status %d for %s: %s", e.StatusCode, e.Path, e.Body)
	}
	return fmt.Sprintf("unexpected status %d for %s", e.StatusCode, e.Path)
}

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
	// FallbackAddrs are where BaseURL's host is dialled when it does not
	// resolve to an address that answers (UCI control_ip), before the
	// built-in KnownAddrs. Ignored like SocketMark.
	FallbackAddrs []string
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
		fb := &fallbackAddrs{configured: opts.FallbackAddrs}
		if u, err := url.Parse(opts.BaseURL); err == nil {
			fb.host = u.Hostname()
		}
		client = &http.Client{Timeout: timeout, Transport: markedTransport(opts.SocketMark, fb), CheckRedirect: noRedirect}
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
func markedTransport(mark int, fb *fallbackAddrs) http.RoundTripper {
	control := setSocketMark(mark)
	if control == nil {
		return nil
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Names resolved on this same marked path (resolve.go), not by dnsmasq:
	// its upstream goes through the tunnel while vctl carries the router.
	r := directResolver(control)
	t.DialContext = resolvingDial(d, r, fb)
	// https: an address answers only with a completed, verified handshake.
	t.DialTLSContext = resolvingTLSDial(d, r, fb, t.TLSClientConfig)
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
	// Register is never signed (signer=nil): the router has no identity yet.
	if err := c.doJSON(ctx, http.MethodPost, "/api/router/register", req, &out, nil); err != nil {
		return RegisterResponse{}, err
	}
	return out, nil
}

// CheckIn reports inventory/health and returns jobs + desired config. When the
// request carries a nonce and device key (SigningNonce/SigningPrivateKey) the
// check-in is signed (A2); otherwise it is byte-identical to a pre-A2 check-in.
func (c *Client) CheckIn(ctx context.Context, req CheckInRequest) (CheckInResponse, error) {
	if req.ProtocolVersion == "" {
		req.ProtocolVersion = ProtocolVersion
	}

	var out CheckInResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/router/check-in", req, &out, c.checkInSigner(req, req.SigningNonce))
	if err == nil {
		return out, nil
	}

	// A2 one-shot re-sign: a 401 carrying code=checkin_signature_required hands
	// back a fresh nonce; re-sign with it and retry exactly once. Any other
	// error (including a 401 for a bad bearer token) is returned unchanged.
	if retryNonce, ok := checkInSignatureRetryNonce(err); ok && req.SigningPrivateKey != "" && retryNonce != "" {
		var retryOut CheckInResponse
		if retryErr := c.doJSON(ctx, http.MethodPost, "/api/router/check-in", req, &retryOut, c.checkInSigner(req, retryNonce)); retryErr != nil {
			return CheckInResponse{}, retryErr
		}
		return retryOut, nil
	}
	return CheckInResponse{}, err
}

// checkInSigner returns a body signer for the A2 headers, or nil when the
// controller holds no nonce or device key (the inert default: no headers are
// added and the request is byte-identical to a pre-A2 controller).
func (c *Client) checkInSigner(req CheckInRequest, nonce string) func(body []byte) (map[string]string, error) {
	if nonce == "" || req.SigningPrivateKey == "" {
		return nil
	}
	routerID := req.SigningRouterID
	if routerID == "" {
		routerID = req.RouterID
	}
	return func(body []byte) (map[string]string, error) {
		signature, nonceHeader, err := SignCheckIn(req.SigningPrivateKey, nonce, routerID, body)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			CheckInSignatureHeader: signature,
			CheckInNonceHeader:     nonceHeader,
		}, nil
	}
}

// checkInSignatureRetryNonce reports the fresh nonce a 401
// checkin_signature_required response carries, if any.
func checkInSignatureRetryNonce(err error) (string, bool) {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		return "", false
	}
	var payload struct {
		Code         string `json:"code"`
		CheckInNonce string `json:"checkInNonce"`
	}
	if json.Unmarshal([]byte(statusErr.Body), &payload) != nil {
		return "", false
	}
	if payload.Code != "checkin_signature_required" {
		return "", false
	}
	return payload.CheckInNonce, true
}

// SubmitJobResult reports the outcome of a delivered job.
func (c *Client) SubmitJobResult(ctx context.Context, req JobResultRequest) (JobResultResponse, error) {
	if req.ProtocolVersion == "" {
		req.ProtocolVersion = ProtocolVersion
	}
	var out JobResultResponse
	// Job results are never signed (signer=nil).
	if err := c.doJSON(ctx, http.MethodPost, "/api/router/job-result", req, &out, nil); err != nil {
		return JobResultResponse{}, err
	}
	return out, nil
}

// doJSON marshals payload ONCE and reuses that one byte slice for both the
// request body and, when signer is non-nil, the signature digest — the panel
// hashes the exact bytes it receives, so the hashed and sent bytes must be the
// same. signer is nil for every call but a signed check-in.
func (c *Client) doJSON(ctx context.Context, method string, path string, payload any, out any, signer func(body []byte) (map[string]string, error)) error {
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
	if signer != nil {
		headers, signErr := signer(body)
		if signErr != nil {
			return fmt.Errorf("sign request: %w", signErr)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		// Preserve the status and (capped) body in a StatusError so the A2
		// one-shot re-sign can read the 401's fresh nonce. StatusError.Error()
		// reproduces the previous human-readable wording for every other path.
		bodyPreview, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return &StatusError{
			StatusCode: response.StatusCode,
			Path:       path,
			Body:       strings.TrimSpace(string(bodyPreview)),
			// The check-in answer may reflect confidential request fields, so its
			// body is kept for the A2 retry but never rendered into the error.
			Confidential: path == "/api/router/check-in",
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		if path == "/api/router/check-in" {
			return fmt.Errorf("invalid check-in response")
		}
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
	if t := markedTransport(mark, &fallbackAddrs{}); t != nil {
		c.Transport = t
	}
	return c
}
