// Package retire takes PassWall2 off a router Vectra has held for good.
//
// The owner, 2026-09-30: Vectra's routers end without PassWall2. The
// takeover (the init script's start) stops and disables PassWall2 and keeps
// it installed as the way back; once vctl has carried the router's traffic,
// switched on for good, for a whole window — a day unless UCI
// passwall_retire_after says otherwise — the daemon removes PassWall2's
// packages: those on an explicit list, and of them only what nothing else on
// the router needs. Its configuration is kept in a backup. From then on
// `vectra off` leaves the router on plain internet: there is nothing left to
// give back, and nothing on the router says there is.
//
// The window runs from the first look at which vctl carried the traffic as
// its switched-on holder (the clock, a file of its own); a hand-back — `vectra
// off`, the dead-man's, a start that declined — takes the clock away, so the
// next takeover waits out a whole window again.
//
// A PassWall2 whose packages a person removed — what docs/CANARY.md once
// said to do — leaves the takeover's breadcrumbs and its configuration
// behind, and no record. After the same window, measured while it is gone
// (so an opkg upgrade that has it absent for seconds never counts), Tidy
// finishes it as a retirement: the same backup, a record that says by hand,
// nothing owed any more.
package retire

// Env is where the router keeps what the retirement reads and changes; tests
// point it at a temp dir and at a fake opkg.
type Env struct {
	// StatusFile and InfoDir are opkg's: /usr/lib/opkg/status and
	// /usr/lib/opkg/info (<package>.control, <package>.list).
	StatusFile, InfoDir string
	// PassWall are PassWall2's init scripts, both spellings, as the init
	// script probes them.
	PassWall []string
	// MarkerDir holds the takeover's breadcrumbs, the record of the
	// retirement and the clock (/etc/vectra-controller-pro); TrialMarkers a
	// trial's breadcrumbs (/tmp/vectra-trial.d), and Snippet the trial's note
	// that turns PassWall2's own switch on at the next boot.
	MarkerDir, TrialMarkers, Snippet string
	// BackupDir is where PassWall2's configuration is kept once it goes, and
	// Configs its files: backed up, and gone with it.
	BackupDir string
	Configs   []string
}

// RouterEnv is the production Env: opkg's files, the init script's
// breadcrumbs (vectra-controller-pro's LEGACY_MARKER_DIR, TRIAL_MARKER_DIR,
// PASSWALL_SWITCH_SNIPPET), PassWall2's init scripts as the init script
// probes them, and its UCI files.
func RouterEnv() Env {
	return Env{
		StatusFile:   "/usr/lib/opkg/status",
		InfoDir:      "/usr/lib/opkg/info",
		PassWall:     []string{"/etc/init.d/passwall2", "/etc/init.d/passwall"},
		MarkerDir:    "/etc/vectra-controller-pro",
		TrialMarkers: "/tmp/vectra-trial.d",
		Snippet:      "/etc/uci-defaults/99-vectra-trial-passwall-switch",
		BackupDir:    "/etc/vectra-controller-pro/backup",
		Configs:      []string{"/etc/config/passwall2", "/etc/config/passwall2_server"},
	}
}
