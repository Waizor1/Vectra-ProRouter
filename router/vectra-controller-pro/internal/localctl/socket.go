package localctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The daemon owns xray, the firewall and the check-in loop; `vctl rpcd` is a
// short-lived process per UI call. The UI socket is how the second asks the
// first: one JSON request, one JSON response, per connection. It is a unix
// socket created 0600 in the daemon's tmpfs run directory, so only root on the
// router can reach it — the same audience as the daemon's own files.

// Socket operations.
const (
	OpRuntime     = "runtime"      // read the daemon's live state
	OpReapply     = "reapply"      // re-render from the cache under the current overrides
	OpRestartXray = "restart_xray" // restart xray on the installed config
	// OpRetirePassWall takes PassWall2 off the router now, when every
	// condition holds (`vctl retire-passwall`, internal/retire).
	OpRetirePassWall = "retire_passwall"
)

// SocketRequest is one call.
type SocketRequest struct {
	Op string `json:"op"`
	// Change is what OpReapply is asked to make true. The daemon persists it
	// only AFTER it has been applied: a change that fails, or that is still
	// queued when the caller gives up waiting, never sits on disk as a choice
	// the router is not running.
	Change *Change `json:"change,omitempty"`
	// Now is OpRetirePassWall's --now: the stability window is not waited
	// out; every other condition still holds.
	Now bool `json:"now,omitempty"`
}

// Change is one edit to the router's local choices.
type Change struct {
	// SetEntry chooses a location; ResetEntry goes back to the panel's.
	SetEntry   *EntryChoice `json:"setEntry,omitempty"`
	ResetEntry bool         `json:"resetEntry,omitempty"`
	// ProbeIntervalSec sets the probe interval; 0 = the router default.
	ProbeIntervalSec *int `json:"probeIntervalSec,omitempty"`
	// SetRules replaces both lists of the owner's sites, already normalized
	// (sites.NormalizeLists — `vctl rpcd` refuses anything else).
	SetRules *Rules `json:"setRules,omitempty"`
	// SetService chooses a service's country; Country "" goes back to the
	// entry's own path.
	SetService *ServiceChoice `json:"setService,omitempty"`
}

// ServiceChoice is one service's country (xray.Services).
type ServiceChoice struct {
	ID      string `json:"id"`
	Country string `json:"country"`
	EntryID string `json:"entryId,omitempty"`
}

// Rules are the owner's own sites ("My sites"): Direct always without the
// VPN, Proxy always through it.
type Rules struct {
	Connect bool     `json:"connect,omitempty"`
	Direct  []string `json:"direct"`
	Proxy   []string `json:"proxy"`
}

// EntryChoice names a location by remark, and by index to tell apart two
// locations the provider gave the same remark.
type EntryChoice struct {
	Digest string `json:"digest,omitempty"`
	Remark string `json:"remark"`
	Index  int    `json:"index"`
}

// TouchesEntry reports whether the change is about the location.
func (c Change) TouchesEntry() bool { return c.SetEntry != nil || c.ResetEntry }

// ApplyTo edits o.
func (c Change) ApplyTo(o *Overrides) {
	switch {
	case c.SetEntry != nil:
		o.EntryDigest = c.SetEntry.Digest
		o.EntryRemark = c.SetEntry.Remark
		i := c.SetEntry.Index
		o.EntryIndex = &i
	case c.ResetEntry:
		o.EntryDigest = ""
		o.EntryRemark, o.EntryIndex = "", nil
	}
	if c.ProbeIntervalSec != nil {
		o.ProbeIntervalSec = *c.ProbeIntervalSec
	}
	if c.SetRules != nil {
		o.ConnectRules = c.SetRules.Connect
		o.Direct = append([]string(nil), c.SetRules.Direct...)
		o.Proxy = append([]string(nil), c.SetRules.Proxy...)
	}
	if s := c.SetService; s != nil {
		delete(o.ServiceEntries, s.ID)
		if s.EntryID != "" {
			if o.ServiceEntries == nil {
				o.ServiceEntries = map[string]string{}
			}
			o.ServiceEntries[s.ID] = s.EntryID
		}
		if s.Country == "" {
			delete(o.Services, s.ID)
		} else {
			if o.Services == nil {
				o.Services = map[string]string{}
			}
			o.Services[s.ID] = s.Country
		}
	}
}

// SocketResponse answers it. Code uses the UI contract's action codes.
type SocketResponse struct {
	OK      bool     `json:"ok"`
	Code    string   `json:"code,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Runtime *Runtime `json:"runtime,omitempty"`
}

// Runtime is the daemon's live state, as only the daemon knows it.
type Runtime struct {
	ControllerPID  int        `json:"controllerPid"`
	StartedAt      time.Time  `json:"startedAt"`
	Version        string     `json:"version"`
	XrayVersion    string     `json:"xrayVersion,omitempty"`
	RouterID       string     `json:"routerId,omitempty"`
	LastCheckIn    *time.Time `json:"lastCheckIn,omitempty"`
	PanelReachable *bool      `json:"panelReachable,omitempty"`
	KillSwitch     bool       `json:"killSwitch"`
	Engine         Engine     `json:"engine"`
	Entry          *Entry     `json:"entry,omitempty"`
	Probe          *Probe     `json:"probe,omitempty"`
	LastApplyError string     `json:"lastApplyError,omitempty"`
	// Busy names the UI operation the daemon is applying right now.
	Busy string `json:"busy,omitempty"`
	// LeakBaseline is the leak counter as it stood once the running xray had
	// started; nil until it is taken, and for any xray but the running one.
	LeakBaseline *LeakBaseline `json:"leakBaseline,omitempty"`
	// Claim is how this router can be linked to a Vectra account (ADR-0006);
	// nil once it is linked (it has an operator config).
	Claim *Claim `json:"claim,omitempty"`
	// Egress is where the router saw each exit really leave (tag → ISO
	// country), from its exit check: «Турция» may leave in Poland.
	Egress map[string]string `json:"egress,omitempty"`
	// Route is where the main balancer sends the LAN's traffic, as the
	// failover watchdog last saw it; nil before its first look.
	Route *Route `json:"route,omitempty"`
}

// Claim is the code the router shows now, ready for the UI.
type Claim struct {
	State     string      `json:"state"` // unclaimed | claimed
	Code      string      `json:"code"`
	QR        string      `json:"qr,omitempty"` // "" until the panel has sent Vectra's key
	ExpiresAt time.Time   `json:"expiresAt"`
	Owner     *ClaimOwner `json:"owner,omitempty"`
}

// ClaimOwner is who claimed the router, as the panel labels them.
type ClaimOwner struct {
	Label string `json:"label"`
}

// LeakBaseline is the vctl table's leak counters as they stood once an xray
// start had settled (internal/firewall). What they held then includes the
// restart window — which falls through by design (dropped with the kill
// switch on) — so only what they add afterwards got past a RUNNING xray.
type LeakBaseline struct {
	// Packets is vctl_would_leak: tcp/udp let out unproxied (switch off).
	Packets int64 `json:"packets"`
	// Escaped is vctl_tproxy_escaped: captured, then forwarded unproxied.
	Escaped int64 `json:"escaped"`
	// Drops is vctl_killswitch_drops: tcp/udp refused (switch on).
	Drops int64 `json:"drops"`
	// TproxyHits only grows while the same ruleset stays loaded: a reading
	// below it means the ruleset was loaded again, and every counter with it.
	TproxyHits int64     `json:"tproxyHits"`
	At         time.Time `json:"at"`
	// XrayPID is the xray it was taken for.
	XrayPID int `json:"xrayPid"`
}

// Engine is the xray supervisor's state.
type Engine struct {
	State        string    `json:"state"`
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	Restarts     int       `json:"restarts"`
	LastExitAt   time.Time `json:"lastExitAt,omitempty"`
	LastExitCode int       `json:"lastExitCode,omitempty"`
	LastExitErr  string    `json:"lastExitErr,omitempty"`
}

// Entry is the location xray is running.
type Entry struct {
	Index     int       `json:"index"`
	Remark    string    `json:"remark"`
	Count     int       `json:"count"`
	Local     bool      `json:"local"`
	Stale     bool      `json:"stale"`
	FetchedAt time.Time `json:"fetchedAt,omitempty"`
}

// Probe is the observatory interval xray is running with.
type Probe struct {
	IntervalSec         int    `json:"intervalSec"`
	ProviderIntervalSec int    `json:"providerIntervalSec"`
	Sampling            int    `json:"sampling"`
	Source              string `json:"source"` // default | local | provider
}

// maxSocketMessage bounds a request or response line.
const maxSocketMessage = 1 << 20

// Handler answers one request. It may block; the connection deadline set by
// the caller bounds how long the client waits for it.
type Handler func(ctx context.Context, req SocketRequest) SocketResponse

// Serve listens on path until ctx ends. A stale socket file from a previous
// daemon is replaced; anything else at that path is left alone and reported.
func Serve(ctx context.Context, path string, h Handler) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("localctl: %s exists and is not a socket; refusing to replace it", path)
		}
		_ = os.Remove(path)
	}
	// umask-independent 0600: create under a private name, chmod, rename.
	tmp := path + ".new"
	_ = os.Remove(tmp)
	ln, err := net.Listen("unix", tmp)
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		ln.Close()
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		ln.Close()
		return err
	}
	// On the way out, remove the socket only if it is still OURS: a daemon
	// that is already starting up again may have replaced it.
	mine, _ := os.Stat(path)
	defer func() {
		if cur, err := os.Stat(path); err == nil && mine != nil && os.SameFile(cur, mine) {
			_ = os.Remove(path)
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	backoff := 50 * time.Millisecond
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			// EMFILE, ENOBUFS under memory pressure: transient on a small
			// router. Ending here would leave the UI with "controller down"
			// until the daemon restarts.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 50 * time.Millisecond
		go serveConn(ctx, conn, h)
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReaderSize(conn, 4096).ReadSlice('\n')
	if err != nil {
		return
	}
	var req SocketRequest
	resp := SocketResponse{}
	if err := json.Unmarshal(line, &req); err != nil {
		resp = SocketResponse{Code: "invalid_params", Detail: "malformed request"}
	} else {
		resp = h(ctx, req)
	}
	out, _ := json.Marshal(resp)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(append(out, '\n'))
}

// ErrDaemonDown is returned by Call when nothing answers on the socket.
var ErrDaemonDown = errors.New("the controller daemon is not running")

// Call sends one request and waits for the answer until ctx ends.
func Call(ctx context.Context, path string, req SocketRequest) (SocketResponse, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return SocketResponse{}, fmt.Errorf("%w (%v)", ErrDaemonDown, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return SocketResponse{}, err
	}
	r := bufio.NewReaderSize(conn, 64*1024)
	line, err := readLine(r)
	if err != nil {
		return SocketResponse{}, err
	}
	var resp SocketResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return SocketResponse{}, fmt.Errorf("localctl: malformed daemon response: %w", err)
	}
	return resp, nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > maxSocketMessage {
			return nil, errors.New("localctl: daemon response too large")
		}
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

// Route is the main balancer as the failover watchdog saw it: what xray
// prefers (Nodes), the override it holds, and the watchdog's own move.
type Route struct {
	Balancer  string     `json:"balancer"`
	Nodes     []string   `json:"nodes"`
	Override  string     `json:"override,omitempty"`
	MovedFrom string     `json:"movedFrom,omitempty"`
	MovedAt   *time.Time `json:"movedAt,omitempty"`
	Reason    string     `json:"reason,omitempty"` // failing | fallback | borrowed
	// Unfit are exits the router's exit check found unable to carry blocked
	// sites that no balancer of the running render selects any more.
	Unfit []string `json:"unfit,omitempty"`
	// UnfitKept are such exits the main balancer still selects: it has no
	// other node (a balancer is never emptied), so blocked sites may fail.
	UnfitKept []string  `json:"unfitKept,omitempty"`
	At        time.Time `json:"at"`
}
