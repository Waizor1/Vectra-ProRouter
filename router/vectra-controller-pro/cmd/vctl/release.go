package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
)

// release is what the router does when the owner who claimed it unbinds it
// in the Vectra app ("released": true, see adoptClaimInfo): it goes back to
// the state it came out of the box in, so that it can be claimed again — by
// the same person or by the next one — with nothing of the previous owner's
// left on it.
//
//   - Traffic first. The data plane is unloaded while the operator config
//     that describes it is still known, and only then is xray stopped: with
//     the kill switch armed, xray gone under a loaded ruleset is a LAN with
//     no internet at all. After both, the LAN goes out directly, as on a
//     router that was never linked.
//   - Then what was the owner's: the operator config (it names the
//     subscription), the provider document and its locations cache (the
//     nodes and their credentials), the rendered xray config, the choices
//     made on the router (location, pins, probe interval, My sites), the
//     applied revision and digest (so the next owner's config applies from
//     scratch), the rescue state, and the owner.
//   - Last, a new claim code: the one the previous owner scanned is not the
//     router's any more.
//
// What is the router's own stays: its identity and panel token, Vectra's key
// and bot, and everything in UCI — Wi-Fi, the root password, setup_done,
// ui_lock. The subscription URL is never logged: the one line says what was
// removed, by name.
func (d *daemon) release(ctx context.Context) {
	var removed []string
	if d.desired != nil && d.unloadDataPlane(ctx, d.desired) {
		removed = append(removed, "the data plane")
	}
	if d.supStarted {
		d.stopXray(ctx)
		removed = append(removed, "xray (stopped)")
	}
	for _, f := range []struct{ what, path string }{
		{"the operator config", d.cfg.XrayConfigPath},
		{"the provider config", d.cfg.ProviderConfigPath},
		{"the PassWall-routing document", passwallDocumentPath(d.cfg.ProviderConfigPath)},
		{"the rendered xray config", d.cfg.XrayRenderPath},
		{"the locations cache", d.cfg.EntriesPath},
		{"the locations index", d.cfg.EntriesIndexPath},
	} {
		switch err := os.Remove(f.path); {
		case err == nil:
			removed = append(removed, f.what)
		case !errors.Is(err, os.ErrNotExist):
			logging.L().Error("could not remove "+f.what+" of the router's previous owner", "path", f.path, "err", err.Error())
		}
	}
	switch ok, err := localctl.ClearOverrides(d.cfg.OverridesPath); {
	case err != nil:
		logging.L().Error("could not remove the choices made on the router", "path", d.cfg.OverridesPath, "err", err.Error())
	case ok:
		removed = append(removed, "the choices made on the router")
	}
	if d.st.AppliedRevisionID != "" || d.st.ConfigDigest != "" || d.st.LastDesiredRevision != nil {
		removed = append(removed, "the applied revision")
	}
	d.st.AppliedRevisionID, d.st.ConfigDigest, d.st.SpliceKey = "", "", ""
	d.st.LastDesiredRevision = nil
	d.st.Rescue = state.RescueSnapshot{}
	d.st.ClaimOwner = nil

	d.desired = nil
	d.rebuildApplier()
	d.nodeCount, d.probe, d.lastApplyErr = 0, nil, ""
	d.leakBaseline.Store(nil)
	d.claim.release()
	removed = append(removed, "the owner")

	logging.L().Info("the owner released this router in the Vectra app: it is as it came out of the box, with a new claim code",
		"removed", strings.Join(removed, ", "))
}
