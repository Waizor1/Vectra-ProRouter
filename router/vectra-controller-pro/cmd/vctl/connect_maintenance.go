package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"vectra-controller-pro/internal/state"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/feedverify"
)

// These seams are fixed-argv operations. Tests never invoke router commands.
var maintenanceCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	out, e := io.ReadAll(io.LimitReader(pipe, (64<<10)+1))
	if len(out) > 64<<10 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("command output too large")
	}
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, e
	}
	return out, cmd.Wait()
}
var maintenanceFeed = trustedFeedIndex
var maintenanceBootID = func() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b)), e
}
var maintenanceNow = time.Now
var maintenancePersist = func(d *daemon) error { return d.persist() }
var maintenanceUpdate = func(d *daemon, ctx context.Context, j controlplane.Job) error { return d.updateController(ctx, j) }

// Persisted preferences and reboot receipt contain no credentials. A changed
// adopted owner never inherits an old owner's automatic-update preference.
type maintenanceState struct {
	OwnerRef            string             `json:"ownerRef"`
	AutoUpdate          bool               `json:"autoUpdate"`
	FeedVerified        bool               `json:"feedVerified"`
	FeedObservedAt      time.Time          `json:"feedObservedAt,omitempty"`
	LastPoll            time.Time          `json:"lastPoll,omitempty"`
	AvailableVersion    string             `json:"availableVersion,omitempty"`
	PreviousVersion     string             `json:"previousVersion,omitempty"`
	ExpectedVersion     string             `json:"expectedVersion,omitempty"`
	UpdateVerified      bool               `json:"updateVerified"`
	RebootVerifiedJobID string             `json:"rebootVerifiedJobId,omitempty"`
	RebootVerifiedAt    time.Time          `json:"rebootVerifiedAt,omitempty"`
	Reboot              *maintenanceReboot `json:"reboot,omitempty"`
}
type maintenanceReboot struct {
	JobID          string `json:"jobId"`
	BootID         string `json:"bootId"`
	Dispatched     bool   `json:"dispatched"`
	Acknowledged   bool   `json:"acknowledged"`
	TerminalStatus string `json:"terminalStatus,omitempty"`
}

func (d *daemon) maintenancePath() string {
	return filepath.Join(filepath.Dir(d.cfg.StatePath), "connect-maintenance.json")
}
func (d *daemon) readMaintenance(owner string) (maintenanceState, error) {
	var s maintenanceState
	f, e := os.OpenFile(d.maintenancePath(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(e, os.ErrNotExist) {
		return maintenanceState{OwnerRef: owner}, nil
	}
	if e != nil {
		return s, errors.New("maintenance state unavailable")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return s, errors.New("unsafe maintenance state")
	}
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil {
		return s, e
	}
	if len(b) > 4096 {
		return s, errors.New("maintenance state too large")
	}
	if e = json.Unmarshal(b, &s); e != nil {
		return s, e
	}
	if s.OwnerRef != owner {
		return maintenanceState{OwnerRef: owner}, nil
	}
	return s, nil
}
func (d *daemon) saveMaintenance(s maintenanceState) error {
	if info, e := os.Lstat(d.maintenancePath()); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return errors.New("unsafe maintenance state")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	dir := filepath.Dir(d.maintenancePath())
	f, e := os.CreateTemp(dir, ".connect-maintenance-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, d.maintenancePath()); e != nil {
		return e
	}
	df, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer df.Close()
	return df.Sync()
}

// connectMaintenance is called only by the authenticated, owner-bound typed
// dispatcher. Params are still checked here so this boundary fails closed.
func (d *daemon) connectMaintenance(ctx context.Context, j controlplane.Job, owner, action string, params map[string]interface{}) error {
	if owner == "" {
		return d.maintenanceFailure(ctx, j, "adopted owner required")
	}
	s, e := d.readMaintenance(owner)
	if e != nil {
		return d.maintenanceFailure(ctx, j, "maintenance state unavailable")
	}
	switch action {
	case "set_auto_update":
		enabled, ok := params["enabled"].(bool)
		if !ok || len(params) != 1 {
			return d.maintenanceFailure(ctx, j, "invalid auto-update parameters")
		}
		s.AutoUpdate = enabled
		if e = d.saveMaintenance(s); e != nil {
			return d.maintenanceFailure(ctx, j, "maintenance preference not saved")
		}
		return d.maintenanceFinish(ctx, j, "success", map[string]interface{}{"autoUpdate": enabled})
	case "update_now":
		if len(params) != 0 {
			return d.maintenanceFailure(ctx, j, "invalid update parameters")
		}
		candidate, e := latestMaintenancePackage(ctx)
		if e != nil {
			return d.maintenanceFailure(ctx, j, "trusted update unavailable")
		}
		if candidate == nil {
			return d.maintenanceFinish(ctx, j, "success", map[string]interface{}{"controllerUpdated": false, "upToDate": true})
		}
		j.Payload = map[string]interface{}{"name": proPackageName, "artifactUrl": candidate.URL, "sha256": candidate.Package.SHA256, "artifactVersion": candidate.Package.Version}
		err := maintenanceUpdate(d, ctx, j)
		if errors.Is(err, errControllerUpToDate) {
			return d.maintenanceFinish(ctx, j, "success", map[string]interface{}{"controllerUpdated": false, "upToDate": true})
		}
		if err == nil {
			return errMaintenanceActionFailed
		}
		return err
	case "reboot":
		if len(params) != 0 {
			return d.maintenanceFailure(ctx, j, "invalid reboot parameters")
		}
		boot, e := maintenanceBootID()
		if e != nil || boot == "" {
			return d.maintenanceFailure(ctx, j, "boot identity unavailable")
		}
		if s.Reboot != nil && s.Reboot.JobID != j.ID {
			return d.maintenanceFailure(ctx, j, "reboot already scheduled")
		}
		if s.Reboot == nil {
			s.Reboot = &maintenanceReboot{JobID: j.ID, BootID: boot}
			if e = d.saveMaintenance(s); e != nil {
				return d.maintenanceFailure(ctx, j, "reboot receipt not saved")
			}
		}
		// This acknowledges durable scheduling, never proof of a physical reboot.
		if err := d.maintenanceFinish(ctx, j, "accepted", map[string]interface{}{"rebootScheduled": true, "rebootVerified": false}); err != nil {
			return errors.Join(errMaintenanceActionPending, err)
		}
		if err := d.connectMaintenanceResultDelivered(owner, j.ID); err != nil {
			return errors.Join(errMaintenanceActionPending, err)
		}
		return errMaintenanceActionPending
	}
	return d.maintenanceFailure(ctx, j, "unsupported maintenance action")
}

type maintenanceCandidate struct {
	Package          feedverify.Package
	URL              string
	InstalledVersion string
}

var safeMaintenanceFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]*\.ipk$`)
var safePackageVersion = regexp.MustCompile(`^[A-Za-z0-9.+:~_-]{1,128}$`)

func newerMaintenanceVersion(ctx context.Context, a, b string) (bool, error) {
	if !safePackageVersion.MatchString(a) || !safePackageVersion.MatchString(b) {
		return false, errors.New("invalid package version")
	}
	if a == b {
		return false, nil
	}
	_, e := maintenanceCommand(ctx, "opkg", "compare-versions", a, ">", b)
	if e == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, e
}
func latestMaintenancePackage(ctx context.Context) (*maintenanceCandidate, error) {
	arch, feed, pkgs, e := maintenanceFeed(ctx)
	if e != nil {
		return nil, e
	}
	if arch == "i386" {
		return nil, errors.New("architecture unsupported")
	}
	installed, e := installedMaintenanceVersion(ctx)
	if e != nil {
		return nil, e
	}
	if e = requireHTTPS(feed.URL); e != nil {
		return nil, e
	}
	var best *maintenanceCandidate
	for _, p := range pkgs {
		if p.Name != proPackageName || p.Architecture != arch {
			continue
		}
		// An authenticated index must still not name a URL/traversal path. Only
		// a filename in this installed feed's directory is accepted.
		if !safeMaintenanceFilename.MatchString(p.Filename) || len(p.Filename) > 255 || strings.ContainsAny(p.Filename, "/\\?#%") || p.Filename == "." || p.Filename == ".." || !strings.HasSuffix(p.Filename, ".ipk") {
			return nil, errors.New("invalid feed filename")
		}
		h, e := hex.DecodeString(p.SHA256)
		if e != nil || len(h) != 32 {
			return nil, errors.New("invalid feed checksum")
		}
		baseline := installed
		if best != nil {
			baseline = best.Package.Version
		}
		newer, e := newerMaintenanceVersion(ctx, p.Version, baseline)
		if e != nil {
			return nil, e
		}
		if !newer {
			continue
		}
		best = &maintenanceCandidate{Package: p, InstalledVersion: installed, URL: strings.TrimRight(feed.URL, "/") + "/" + url.PathEscape(p.Filename)}
	}
	return best, nil
}

// Call after a successful authenticated check-in, after recoverJournal. The
// hook is owner-bound and persists intent before side effects. A failed poll
// is bounded to once/day; a manual update remains available immediately.
func (d *daemon) connectMaintenanceAfterCheckin(ctx context.Context, owner string) error {
	if owner == "" {
		return nil
	}
	s, e := d.readMaintenance(owner)
	if e != nil {
		return e
	}
	if s.Reboot != nil {
		if s.Reboot.TerminalStatus != "" {
			return d.maintenanceRebootTerminal(ctx, owner, s.Reboot.JobID, s.Reboot.TerminalStatus)
		}
		boot, e := maintenanceBootID()
		if e != nil || boot == "" {
			return errors.New("boot identity unavailable")
		}
		if boot != s.Reboot.BootID {
			s.RebootVerifiedJobID = s.Reboot.JobID
			s.RebootVerifiedAt = maintenanceNow()
			s.Reboot.TerminalStatus = "success"
			if e = d.saveMaintenance(s); e != nil {
				return e
			}
			return d.maintenanceRebootTerminal(ctx, owner, s.Reboot.JobID, "success")
		}
		if !s.Reboot.Acknowledged || s.Reboot.Dispatched || d.st.PendingJobResult != nil || d.st.CurrentJob.JobID != "" {
			return nil
		}
		s.Reboot.Dispatched = true
		if e = d.saveMaintenance(s); e != nil {
			return e
		}
		// At-most-once dispatch: a crash after command delivery must not reboot
		// repeatedly. Same-boot dispatched receipt remains visible for recovery.
		_, e = maintenanceCommand(ctx, "/sbin/reboot")
		if e != nil {
			s.Reboot.TerminalStatus = "failure"
			if saveErr := d.saveMaintenance(s); saveErr != nil {
				return saveErr
			}
			return d.maintenanceRebootTerminal(ctx, owner, s.Reboot.JobID, "failure")
		}
		return nil
	}
	if d.st.PendingJobResult != nil || d.st.CurrentJob.JobID != "" {
		return nil
	}
	if s.ExpectedVersion != "" {
		installed, e := installedMaintenanceVersion(ctx)
		if e != nil {
			return errors.New("automatic installed version verification unavailable")
		}
		if installed == s.ExpectedVersion && maintenanceRuntimeMatches(runtimeVersion, s.ExpectedVersion) {
			s.UpdateVerified = true
			s.ExpectedVersion = ""
			s.PreviousVersion = ""
			if e = d.saveMaintenance(s); e != nil {
				return e
			}
		} else {
			// Do not overlap an earlier attempt. Reconcile no more than daily.
			now := maintenanceNow()
			if now.Sub(s.LastPoll) < 24*time.Hour {
				return errors.New("automatic update awaiting version verification")
			}
			if installed == s.ExpectedVersion {
				s.LastPoll = now
				if e = d.saveMaintenance(s); e != nil {
					return e
				}
				return maintenanceAutoRestart()
			}
			if s.PreviousVersion == "" || installed != s.PreviousVersion {
				return errors.New("automatic install state requires reconciliation")
			}
			// Installed old version proves the interrupted attempt did not advance.
			// A fresh signature/arch/version verification below precedes a bounded retry.
			s.ExpectedVersion = ""
			s.PreviousVersion = ""
			if e = d.saveMaintenance(s); e != nil {
				return e
			}
		}
	}
	now := maintenanceNow()
	if !s.LastPoll.IsZero() && now.Sub(s.LastPoll) < 24*time.Hour {
		return nil
	}
	s.LastPoll = now
	s.FeedVerified = false
	if e = d.saveMaintenance(s); e != nil {
		return e
	}
	candidate, e := latestMaintenancePackage(ctx)
	if e != nil {
		return errors.New("automatic trusted update check failed")
	}
	s.FeedVerified = true
	s.FeedObservedAt = now
	if candidate == nil {
		s.AvailableVersion = ""
		return d.saveMaintenance(s)
	}
	s.AvailableVersion = candidate.Package.Version
	if !s.AutoUpdate {
		return d.saveMaintenance(s)
	}
	s.PreviousVersion = candidate.InstalledVersion
	s.ExpectedVersion = candidate.Package.Version
	s.UpdateVerified = false
	// Journal intent before install, so an interruption remains observable.
	if e = d.saveMaintenance(s); e != nil {
		return e
	}
	return maintenanceAutoInstall(ctx, *candidate)

}

// Delivery acknowledgement is persisted separately so reboot cannot precede
// successful result delivery, including a retried result after interruption.
func (d *daemon) connectMaintenanceResultDelivered(owner, jobID string) error {
	s, e := d.readMaintenance(owner)
	if e != nil {
		return e
	}
	if s.Reboot == nil || s.Reboot.JobID != jobID {
		return nil
	}
	if s.Reboot.TerminalStatus != "" {
		s.Reboot = nil
		return d.saveMaintenance(s)
	}
	s.Reboot.Acknowledged = true
	return d.saveMaintenance(s)
}

var maintenanceAutoInstall = func(ctx context.Context, c maintenanceCandidate) error {
	f, e := os.CreateTemp("", "vectra-controller-pro-auto-*.ipk")
	if e != nil {
		return e
	}
	dest := f.Name()
	f.Close()
	defer os.Remove(dest)
	sha, e := downloadFile(ctx, c.URL, dest)
	if e != nil {
		return errors.New("automatic package download failed")
	}
	if !strings.EqualFold(sha, c.Package.SHA256) {
		return errors.New("automatic package checksum mismatch")
	}
	installCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	if _, e = runControllerInstall(installCtx, dest); e != nil {
		return errors.New("automatic package install failed")
	}
	scheduleControllerRestart()
	return errControllerRestartRequested
}

// maintenanceSnapshot exposes no password or implementation command output.
func (d *daemon) maintenanceSnapshot(owner string) (bool, string, error) {
	s, e := d.readMaintenance(owner)
	return s.AutoUpdate, s.AvailableVersion, e
}

// errMaintenanceActionFailed distinguishes a delivered failure from execution
// success; successful HTTP result delivery does not turn a rejected action green.
var errMaintenanceActionFailed = errors.New("maintenance action failed")

func (d *daemon) maintenanceFailure(ctx context.Context, j controlplane.Job, msg string) error {
	if e := d.maintenanceFinish(ctx, j, "failure", map[string]interface{}{"error": msg}); e != nil {
		return errors.Join(errMaintenanceActionFailed, e)
	}
	return errMaintenanceActionFailed
}
func (d *daemon) maintenanceFinish(ctx context.Context, j controlplane.Job, status string, result map[string]interface{}) error {
	req := controlplane.JobResultRequest{ProtocolVersion: controlplane.ProtocolVersion, RouterID: d.st.RouterID, JobID: j.ID, Status: status, Result: result}
	d.st.PendingJobResult = &req
	d.st.CurrentJob = state.CurrentJob{}
	if e := maintenancePersist(d); e != nil {
		return errors.New("maintenance result not saved")
	}
	if _, e := d.client.SubmitJobResult(ctx, req); e != nil {
		return errors.New("maintenance result delivery pending")
	}
	d.st.PendingJobResult = nil
	if e := maintenancePersist(d); e != nil {
		d.st.PendingJobResult = &req
		return errors.New("maintenance acknowledgement not saved")
	}
	return nil
}

func installedMaintenanceVersion(ctx context.Context) (string, error) {
	out, e := maintenanceCommand(ctx, "opkg", "status", proPackageName)
	if e != nil {
		return "", e
	}
	if len(out) > 64<<10 {
		return "", errors.New("package status too large")
	}
	installed := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Version: ") {
			installed = strings.TrimPrefix(line, "Version: ")
		}
	}
	if !safePackageVersion.MatchString(installed) {
		return "", errors.New("installed package version unavailable")
	}
	return installed, nil
}

// Runtime build uses version-r<release>; opkg uses version-<release>.
// Only that final numeric release spelling is normalized, never loose prefix.
var runtimeReleaseSuffix = regexp.MustCompile(`-r([0-9]+)$`)

func maintenanceRuntimeMatches(runtime, expected string) bool {
	return runtime == expected || runtimeReleaseSuffix.ReplaceAllString(runtime, "-$1") == expected
}

var maintenanceAutoRestart = func() error { scheduleControllerRestart(); return errControllerRestartRequested }

func maintenanceConfirmVersion(ctx context.Context, expected string) (bool, error) {
	installed, e := installedMaintenanceVersion(ctx)
	return e == nil && installed == expected && maintenanceRuntimeMatches(runtimeVersion, expected), e
}

// Capabilities require a recent successful signed observation for this owner,
// including an up-to-date feed. Merely finding config/key files is insufficient.
func (d *daemon) maintenanceFeedAvailable(owner string) bool {
	if owner == "" {
		return false
	}
	s, e := d.readMaintenance(owner)
	if e != nil || !s.FeedVerified || s.FeedObservedAt.IsZero() {
		return false
	}
	age := maintenanceNow().Sub(s.FeedObservedAt)
	pollAge := maintenanceNow().Sub(s.LastPoll)
	return age >= 0 && age <= 24*time.Hour && !s.LastPoll.IsZero() && pollAge >= 0 && pollAge <= 24*time.Hour
}

// A durable boot-ID change is later physical proof, separate from the earlier
// scheduling acknowledgment. It is never inherited across adopted owners.
func (d *daemon) maintenanceRebootProof(owner string) (string, time.Time, error) {
	s, e := d.readMaintenance(owner)
	return s.RebootVerifiedJobID, s.RebootVerifiedAt, e
}

var errMaintenanceActionPending = errors.New("maintenance action pending")

func (d *daemon) maintenancePendingReboot(owner, jobID string) bool {
	s, e := d.readMaintenance(owner)
	return owner != "" && (e != nil || (s.Reboot != nil && s.Reboot.JobID == jobID))
}
func (d *daemon) maintenanceRebootTerminal(ctx context.Context, owner, jobID, status string) error {
	result := map[string]interface{}{"rebootVerified": status == "success"}
	if status == "failure" {
		result["error"] = "scheduled reboot command failed"
	}
	if e := d.maintenanceFinish(ctx, controlplane.Job{ID: jobID}, status, result); e != nil {
		return errors.Join(errMaintenanceActionPending, e)
	}
	if e := d.connectRecoverDelivered(jobID, status); e != nil {
		return errors.Join(errMaintenanceActionPending, e)
	}
	if e := d.connectMaintenanceResultDelivered(owner, jobID); e != nil {
		return e
	}
	if status == "failure" {
		return errMaintenanceActionFailed
	}
	return nil
}
