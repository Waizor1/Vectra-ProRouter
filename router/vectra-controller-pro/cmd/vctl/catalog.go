package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/logging"
)

// The catalog (internal/catalog, docs/superpowers/specs/2026-10-17-vctl-
// catalog.md): what vctl knows about services and sites besides the
// provider and the owner, changed by publishing a signed catalog.json next
// to the Vectra feed — not by a release. The daemon asks for it at its first
// poll and every catalog.Refresh hours; `vctl catalog refresh` asks now; a
// catalog stored by anything (that command, `vctl catalog reset`) is taken
// at the next poll. A newer catalog joins the render key, so the running
// document is rendered again and xray reloaded the usual way.

func init() {
	register(command{name: "catalog", summary: "the catalog: show | refresh | reset | check <file>", run: cmdCatalog})
}

var (
	// catalogHTTPClient is the catalog's client: on the control plane's marked
	// path, so the request leaves the router whether or not xray carries
	// anything. Tests trust their own server with it.
	catalogHTTPClient = func() *http.Client {
		return controlplane.MarkedHTTPClient(firewall.DefaultControlMark, 30*time.Second)
	}
	// catalogRetryAfter: a failed ask is tried again this soon; what runs
	// keeps running meanwhile.
	catalogRetryAfter = 30 * time.Minute
)

// useCatalog loads the catalog in use from disk and gives it to the splice:
// every command that renders or reports services calls it at start.
func useCatalog(cfg agentcfg.Config) catalog.Loaded {
	l := catalog.Load(cfg.CatalogPath)
	xray.UseCatalog(l.Catalog)
	if l.Note != "" && !l.Older {
		logging.L().Warn("the stored catalog is not used; the built-in one is", "note", l.Note)
	}
	return l
}

// catalogURL is catalog.json in the channel of the signed Vectra feed: the
// feed's URL names one architecture's directory, the catalog sits above it.
func catalogURL(feedURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(feedURL, "/"))
	if err != nil {
		return "", err
	}
	u.Path = path.Join(path.Dir(u.Path), "catalog.json")
	return u.String(), nil
}

// fetchCatalog asks the Vectra feed's channel for its catalog over client
// and checks it: signed by the key that signs the feed (an OpenWrt key in
// /etc/opkg/keys vouches for packages, not for routing), within its caps,
// of a schema this vctl reads.
func fetchCatalog(ctx context.Context, client *http.Client) (*catalog.Catalog, []byte, []byte, error) {
	fetch := func(ctx context.Context, u string, max int64) ([]byte, error) {
		return fetchHTTPSWith(ctx, client, u, max)
	}
	_, feed, _, key, err := trustedFeedIndexWith(ctx, fetch)
	if err != nil {
		return nil, nil, nil, err
	}
	u, err := catalogURL(feed.URL)
	if err != nil {
		return nil, nil, nil, err
	}
	raw, err := fetch(ctx, u, catalog.MaxBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("catalog.json: %w", err)
	}
	sig, err := fetch(ctx, u+".sig", catalog.MaxSignatureBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("catalog.json.sig: %w", err)
	}
	if err := catalog.Verify(raw, sig, opkgKeysDir, key); err != nil {
		return nil, nil, nil, err
	}
	c, err := catalog.Parse(raw, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return c, raw, sig, nil
}

// refreshCatalog asks for the channel's catalog and stores it when it is
// newer than the one in use; stored says so. A catalog no newer than the
// one in use is no error (the release may carry a newer one); one with the
// serial in use and other content is (a replay or a mistake).
func refreshCatalog(ctx context.Context, cfg agentcfg.Config, client *http.Client) (next *catalog.Catalog, stored bool, err error) {
	cur := catalog.Load(cfg.CatalogPath)
	next, raw, sig, err := fetchCatalog(ctx, client)
	if err != nil {
		return nil, false, err
	}
	switch err := catalog.Newer(cur.Catalog, cur.Raw, next, raw); {
	case err == nil:
	case errors.Is(err, catalog.ErrSame), errors.Is(err, catalog.ErrOlder):
		return next, false, nil
	default:
		return next, false, err
	}
	if err := catalog.Store(cfg.CatalogPath, raw, sig); err != nil {
		return next, false, err
	}
	return next, true, nil
}

// maybeRefreshCatalog runs every poll: it takes a catalog stored since the
// last render, and asks the feed when that is due.
func (d *daemon) maybeRefreshCatalog(ctx context.Context, now time.Time) {
	if l := catalog.Load(d.cfg.CatalogPath); l.Catalog.Serial != d.catalogSerial {
		d.applyCatalog(ctx, l)
	}
	d.maybeRetryRerender(ctx, now)
	if !d.nextCatalogRefresh.IsZero() && now.Before(d.nextCatalogRefresh) {
		return
	}
	next, stored, err := refreshCatalog(ctx, d.cfg, catalogHTTPClient())
	if errors.Is(err, errHTTPNotFound) || errors.Is(err, catalog.ErrReplay) {
		// Nothing published on the channel, or a catalog with the serial in
		// use and other bytes (a release's own catalog republished): nothing
		// newer — asked again at the usual time, not every half hour.
		d.nextCatalogRefresh = now.Add(catalogRefreshPeriod(xray.ActiveCatalog().Refresh()))
		logging.L().Debug("no newer catalog on the channel", "reason", err.Error(), "serial", d.catalogSerial)
		return
	}
	if err != nil {
		d.nextCatalogRefresh = now.Add(catalogRetryAfter)
		logging.L().Warn("the catalog could not be refreshed; the one in use stays", "err", err.Error(),
			"serial", d.catalogSerial, "retryIn", catalogRetryAfter.String())
		return
	}
	d.nextCatalogRefresh = now.Add(catalogRefreshPeriod(next.Refresh()))
	if stored {
		logging.L().Info("a newer catalog arrived", "serial", next.Serial)
		d.applyCatalog(ctx, catalog.Load(d.cfg.CatalogPath))
	}
}

// catalogRefreshPeriod is hours ±10 %, so a fleet does not ask at once.
func catalogRefreshPeriod(hours int) time.Duration {
	p := time.Duration(hours) * time.Hour
	return p - p/10 + time.Duration(rand.Int64N(int64(p/5)+1))
}

// applyCatalog makes l the catalog the renders apply and renders the running
// document again under it (the render key carries the serial). A render the
// catalog breaks is refused like any other; what runs keeps running.
func (d *daemon) applyCatalog(ctx context.Context, l catalog.Loaded) {
	prev := d.catalogSerial
	xray.UseCatalog(l.Catalog)
	d.catalogSerial = l.Catalog.Serial
	logging.L().Info("catalog in use", "serial", l.Catalog.Serial, "source", string(l.Source), "was", prev)
	if l.Note != "" && !l.Older {
		logging.L().Warn("the stored catalog is not used; the built-in one is", "note", l.Note)
	}
	if d.desired == nil || d.applier == nil || d.applier.Tproxy == nil || !fileExists(d.cfg.XrayRenderPath) {
		return // nothing runs yet: the first render takes it
	}
	if r := d.rerenderRunning(ctx); !r.OK {
		logging.L().Warn("the catalog's render was refused; what runs keeps running", "serial", l.Catalog.Serial, "detail", r.Detail)
		d.rerenderRetryAt = time.Now().Add(rerenderRetryAfter)
	}
}

// rerenderRetryAfter: a render a new catalog or geo set asked for and the
// gate refused — most often «not now», too little memory for xray -test — is
// tried again this soon, not left for the next reboot.
var rerenderRetryAfter = 10 * time.Minute

// maybeRetryRerender renders the running document again when a refused
// render is due another try; nothing when none is.
func (d *daemon) maybeRetryRerender(ctx context.Context, now time.Time) {
	if d.rerenderRetryAt.IsZero() || now.Before(d.rerenderRetryAt) {
		return
	}
	if r := d.rerenderRunning(ctx); !r.OK {
		d.rerenderRetryAt = now.Add(rerenderRetryAfter)
		logging.L().Warn("the render the catalog or the geo set asked for is still refused", "detail", r.Detail)
		return
	}
	d.rerenderRetryAt = time.Time{}
}

// catalogStatus is what `vctl catalog show` prints.
type catalogStatus struct {
	Serial   int64             `json:"serial"`
	Source   catalog.Source    `json:"source"`
	Note     string            `json:"note,omitempty"`
	Services map[string]string `json:"services"`
	AIExits  [][]string        `json:"aiExits"`
	Sites    catalog.Sites     `json:"sites"`
	Refresh  int               `json:"refreshHours"`
}

func cmdCatalog(args []string) error {
	fs := newFlagSet("catalog")
	cfgPath := fs.String("config", rpcdAgentConfig, "the daemon's agent config")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sub := "show"
	if fs.NArg() > 0 {
		sub = fs.Arg(0)
	}
	if sub == "check" {
		// The publisher's check: the current code's parser, strict.
		if fs.NArg() != 2 {
			return errors.New("catalog check <file>")
		}
		raw, err := os.ReadFile(fs.Arg(1))
		if err != nil {
			return err
		}
		c, err := catalog.Parse(raw, true)
		if err != nil {
			return err
		}
		fmt.Printf("ok: catalog %d\n", c.Serial)
		return nil
	}
	cfg, err := agentcfg.Load(*cfgPath)
	if err != nil {
		return err
	}
	switch sub {
	case "show":
		return printCatalog(os.Stdout, catalog.Load(cfg.CatalogPath), *asJSON)
	case "refresh":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		next, stored, err := refreshCatalog(ctx, cfg, catalogHTTPClient())
		if err != nil {
			return err
		}
		if stored {
			fmt.Printf("stored catalog %d; the daemon renders with it at its next poll\n", next.Serial)
		} else {
			fmt.Printf("the channel's catalog %d is not newer than the one in use (%d)\n", next.Serial, catalog.Load(cfg.CatalogPath).Catalog.Serial)
		}
		return nil
	case "reset":
		if err := catalog.Remove(cfg.CatalogPath); err != nil {
			return err
		}
		fmt.Printf("the downloaded catalog is gone; the built-in %d is in use at the daemon's next poll\n", catalog.Builtin().Serial)
		return nil
	}
	return fmt.Errorf("catalog: unknown subcommand %q (show | refresh | reset | check <file>)", sub)
}

func printCatalog(w io.Writer, l catalog.Loaded, asJSON bool) error {
	st := catalogStatus{Serial: l.Catalog.Serial, Source: l.Source, Note: l.Note, Services: map[string]string{},
		AIExits: l.Catalog.AIExits(), Sites: l.Catalog.Sites, Refresh: l.Catalog.Refresh()}
	for id, s := range l.Catalog.Services {
		mode := "the provider's list"
		if s.OnlyKnown {
			mode = "only its own names"
		}
		st.Services[id] = fmt.Sprintf("%s, +%d names, %d geosites", mode, len(s.Names), len(s.Geosites))
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Fprintf(w, "catalog %d (%s)\n", st.Serial, st.Source)
	if st.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", st.Note)
	}
	for id, s := range st.Services {
		fmt.Fprintf(w, "  service %s: %s\n", id, s)
	}
	fmt.Fprintf(w, "  «Нейросети» by default: %v\n", st.AIExits)
	fmt.Fprintf(w, "  sites: %d direct, %d proxy\n", len(st.Sites.Direct), len(st.Sites.Proxy))
	fmt.Fprintf(w, "  asks every %d h\n", st.Refresh)
	return nil
}
