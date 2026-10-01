package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/feedverify"
)

func maintenanceFixture(t *testing.T) *daemon {
	t.Helper()
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	oldPersist := maintenancePersist
	t.Cleanup(func() { maintenancePersist = oldPersist })
	command, feed, boot, now, install := maintenanceCommand, maintenanceFeed, maintenanceBootID, maintenanceNow, maintenanceAutoInstall
	t.Cleanup(func() {
		maintenanceCommand = command
		maintenanceFeed = feed
		maintenanceBootID = boot
		maintenanceNow = now
		maintenanceAutoInstall = install
	})
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return "", feedverify.Feed{}, nil, errors.New("fake unavailable feed")
	}
	maintenanceCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unexpected command")
		return nil, nil
	}
	maintenanceBootID = func() (string, error) { return "boot-a", nil }
	maintenanceNow = func() time.Time { return time.Unix(2000000000, 0) }
	return d
}
func TestMaintenancePreferenceOwnerAndMode(t *testing.T) {
	d := maintenanceFixture(t)
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", AutoUpdate: true}); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(d.maintenancePath())
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, e)
	}
	s, e := d.readMaintenance("b")
	if e != nil || s.AutoUpdate || s.OwnerRef != "b" {
		t.Fatalf("old owner preference inherited %+v %v", s, e)
	}
}
func TestMaintenanceRebootAcknowledgementAndAtMostOnce(t *testing.T) {
	d := maintenanceFixture(t)
	s := maintenanceState{OwnerRef: "a", Reboot: &maintenanceReboot{JobID: "j", BootID: "boot-a"}}
	if e := d.saveMaintenance(s); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e := d.connectMaintenanceAfterCheckin(ctx, "a"); e != nil {
		t.Fatal(e)
	} // no ack, no command
	if e := d.connectMaintenanceResultDelivered("a", "other"); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceAfterCheckin(ctx, "a"); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceResultDelivered("a", "j"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "/sbin/reboot" || len(args) != 0 {
			t.Fatal(name, args)
		}
		return nil, nil
	}
	for i := 0; i < 3; i++ {
		if e := d.connectMaintenanceAfterCheckin(ctx, "a"); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 {
		t.Fatalf("replayed reboot %d", calls)
	}
	maintenanceBootID = func() (string, error) { return "boot-b", nil }
	if e := d.connectMaintenanceAfterCheckin(ctx, "a"); e != nil {
		t.Fatal(e)
	}
	recovered, e := d.readMaintenance("a")
	if e != nil || recovered.Reboot != nil || recovered.RebootVerifiedJobID != "j" || recovered.RebootVerifiedAt.IsZero() {
		t.Fatal("boot proof not recorded", e)
	}
}
func TestMaintenanceRebootInterruptedCommandIsNotRepeated(t *testing.T) {
	d := maintenanceFixture(t)
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", Reboot: &maintenanceReboot{JobID: "j", BootID: "boot-a", Acknowledged: true}}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	maintenanceCommand = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("fake failure")
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e == nil {
		t.Fatal("failure hidden")
	}
	if d.maintenancePendingReboot("a", "j") {
		t.Fatal("terminal command failure remained pending")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func fakeMaintenanceFeed(filename string) (string, feedverify.Feed, []feedverify.Package, error) {
	return "aarch64_generic", feedverify.Feed{URL: "https://feed.example/aarch64_generic"}, []feedverify.Package{{Name: proPackageName, Version: "0.6.0-r38", Architecture: "aarch64_generic", Filename: filename, SHA256: strings.Repeat("a", 64)}}, nil
}
func TestMaintenanceCandidateRejectsTraversalAndUnsignedFeed(t *testing.T) {
	maintenanceFixture(t)
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "opkg" {
			t.Fatal(name)
		}
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	for _, filename := range []string{"../vectra.ipk", "https://evil/ipk", "a%2f.ipk", "a\\b.ipk", "a.ipk?x", "a.ipk#x", "not-ipk", "a\n.ipk", "a b.ipk", "a;.ipk"} {
		maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
			return fakeMaintenanceFeed(filename)
		}
		if _, e := latestMaintenancePackage(context.Background()); e == nil {
			t.Fatalf("accepted %q", filename)
		}
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return "", feedverify.Feed{}, nil, errors.New("signature invalid")
	}
	if _, e := latestMaintenancePackage(context.Background()); e == nil {
		t.Fatal("unverified index accepted")
	}
}
func TestMaintenanceCandidateFixedCommandsAndOwnPackage(t *testing.T) {
	maintenanceFixture(t)
	calls := 0
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "opkg" {
			t.Fatal(name)
		}
		if len(args) == 2 && args[0] == "status" && args[1] == proPackageName {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		if len(args) != 4 || args[0] != "compare-versions" || args[1] != "0.6.0-r38" || args[2] != ">" || args[3] != "0.6.0-r37" {
			t.Fatal(args)
		}
		return nil, nil
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return fakeMaintenanceFeed("vectra-controller-pro_0.6.0-r38_aarch64_generic.ipk")
	}
	c, e := latestMaintenancePackage(context.Background())
	if e != nil || c == nil || !strings.HasPrefix(c.URL, "https://feed.example/aarch64_generic/") {
		t.Fatalf("%+v %v", c, e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		a, f, p, e := fakeMaintenanceFeed("legacy.ipk")
		p[0].Name = "vectra-controller"
		return a, f, p, e
	}
	c, e = latestMaintenancePackage(context.Background())
	if e != nil || c != nil {
		t.Fatal("legacy offered", e)
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		a, f, p, e := fakeMaintenanceFeed("own.ipk")
		a = "i386"
		return a, f, p, e
	}
	if _, e = latestMaintenancePackage(context.Background()); e == nil {
		t.Fatal("i386 accepted")
	}
}
func TestMaintenanceAutomaticPollBoundedAndInterrupted(t *testing.T) {
	d := maintenanceFixture(t)
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", AutoUpdate: true}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		calls++
		return "", feedverify.Feed{}, nil, errors.New("offline")
	}
	for i := 0; i < 3; i++ {
		_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	}
	if calls != 1 {
		t.Fatal("poll unbounded", calls)
	}
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "b")
	if calls != 2 {
		t.Fatal("new owner discovery missing")
	}
	maintenanceCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("Version: 0.6.0-r37\n"), nil }
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", AutoUpdate: true, ExpectedVersion: "0.6.0-r38"}); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e == nil {
		t.Fatal("interrupted install silently retried")
	}
	if calls != 2 {
		t.Fatal("interrupted install repeated")
	}
}

func TestMaintenanceDiscoveryWithAutoDisabled(t *testing.T) {
	d := maintenanceFixture(t)
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return fakeMaintenanceFeed("own.ipk")
	}
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	maintenanceAutoInstall = func(context.Context, maintenanceCandidate) error {
		t.Fatal("disabled auto-update installed")
		return nil
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	enabled, version, e := d.maintenanceSnapshot("a")
	if e != nil || enabled || version != "0.6.0-r38" {
		t.Fatal(enabled, version, e)
	}
}
func TestMaintenanceRejectsUnsafeState(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "permissive"} {
		t.Run(kind, func(t *testing.T) {
			d := maintenanceFixture(t)
			switch kind {
			case "symlink":
				if e := os.Symlink(filepath.Join(t.TempDir(), "target"), d.maintenancePath()); e != nil {
					t.Fatal(e)
				}
			case "directory":
				_ = os.Mkdir(d.maintenancePath(), 0700)
			case "permissive":
				_ = os.WriteFile(d.maintenancePath(), []byte(`{}`), 0644)
			}
			if _, e := d.readMaintenance("a"); e == nil {
				t.Fatal("unsafe read accepted")
			}
			if e := d.saveMaintenance(maintenanceState{OwnerRef: "a"}); e == nil {
				t.Fatal("unsafe save accepted")
			}
		})
	}
}
func TestMaintenanceDeliveredFailureRemainsFailure(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	e := d.connectMaintenance(context.Background(), controlplane.Job{ID: "invalid"}, "a", "set_auto_update", map[string]interface{}{"enabled": "yes"})
	if !errors.Is(e, errMaintenanceActionFailed) {
		t.Fatal("delivered failure classified successful", e)
	}
	r, ok := lastResult(results, &mu, "invalid")
	if !ok || r.Status != "failure" {
		t.Fatal(r, ok)
	}
}
func TestMaintenanceRebootRequiresSavedResult(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	oldBoot := maintenanceBootID
	t.Cleanup(func() { maintenanceBootID = oldBoot })
	maintenanceBootID = func() (string, error) { return "boot-a", nil }
	oldPersist := maintenancePersist
	t.Cleanup(func() { maintenancePersist = oldPersist })
	maintenancePersist = func(*daemon) error { return errors.New("fake disk full") }
	e := d.connectMaintenance(context.Background(), controlplane.Job{ID: "reboot"}, "a", "reboot", nil)
	if e == nil {
		t.Fatal("unsaved result accepted")
	}
	s, e := d.readMaintenance("a")
	if e != nil || s.Reboot == nil || s.Reboot.Acknowledged {
		t.Fatal("reboot acknowledged before durable result", s, e)
	}
	if _, ok := lastResult(results, &mu, "reboot"); ok {
		t.Fatal("result delivered before durability")
	}
}
func TestMaintenanceCandidateRealSignatureVerification(t *testing.T) {
	maintenanceFixture(t)
	stand := newSignedFeedStand(t)
	maintenanceFeed = trustedFeedIndex
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r36\n"), nil
		}
		return nil, nil
	}
	c, e := latestMaintenancePackage(context.Background())
	if e != nil || c == nil || c.Package.SHA256 != stand.sha {
		t.Fatal(c, e)
	}
	stand.serveIndex([]byte(feedStanza(proPackageName, feedVersion, feedArch, stand.sha)), []byte("forged"))
	if _, e := latestMaintenancePackage(context.Background()); e == nil {
		t.Fatal("forged signature accepted")
	}
	if len(stand.installed) != 0 || stand.restarts != 0 {
		t.Fatal("discovery invoked install")
	}
}
func TestMaintenanceAutomaticInstallReceiptNoPanelJob(t *testing.T) {
	d := maintenanceFixture(t)
	oldVersion := runtimeVersion
	t.Cleanup(func() { runtimeVersion = oldVersion })
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return fakeMaintenanceFeed("own.ipk")
	}
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	installs := 0
	maintenanceAutoInstall = func(context.Context, maintenanceCandidate) error { installs++; return errControllerRestartRequested }
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", AutoUpdate: true}); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); !errors.Is(e, errControllerRestartRequested) {
		t.Fatal(e)
	}
	if d.st.PendingJobResult != nil || d.st.CurrentJob.JobID != "" {
		t.Fatal("synthetic panel job created")
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e == nil {
		t.Fatal("interrupted update not surfaced")
	}
	if installs != 1 {
		t.Fatal("install replay", installs)
	}
	runtimeVersion = "0.6.0-r38"
	maintenanceCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("Version: 0.6.0-r38\n"), nil }
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	s, e := d.readMaintenance("a")
	if e != nil || !s.UpdateVerified || s.ExpectedVersion != "" {
		t.Fatal(s, e)
	}
}

func TestMaintenanceVerifiesRealOpkgReleaseAgainstRuntime(t *testing.T) {
	d := maintenanceFixture(t)
	old := runtimeVersion
	t.Cleanup(func() { runtimeVersion = old })
	runtimeVersion = "0.6.0-r38"
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "opkg" || len(args) != 2 || args[0] != "status" || args[1] != proPackageName {
			t.Fatal(name, args)
		}
		return []byte("Version: 0.6.0-38\n"), nil
	}
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", ExpectedVersion: "0.6.0-38", LastPoll: maintenanceNow()}); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	s, e := d.readMaintenance("a")
	if e != nil || !s.UpdateVerified || s.ExpectedVersion != "" {
		t.Fatal(s, e)
	}
	for _, v := range []string{"0.6.0-r380", "0.6.0-r38-extra", "0.6.0-r37"} {
		if maintenanceRuntimeMatches(v, "0.6.0-38") {
			t.Fatal("loose version proof", v)
		}
	}
}

func TestMaintenanceManualUpdateDeliveredFailureNotSuccessful(t *testing.T) {
	maintenanceFixture(t)
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return fakeMaintenanceFeed("own.ipk")
	}
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	old := maintenanceUpdate
	t.Cleanup(func() { maintenanceUpdate = old })
	maintenanceUpdate = func(d *daemon, ctx context.Context, j controlplane.Job) error {
		return d.submitFailure(ctx, j, "fake install refused")
	}
	if e := d.connectMaintenance(context.Background(), controlplane.Job{ID: "u"}, "a", "update_now", nil); !errors.Is(e, errMaintenanceActionFailed) {
		t.Fatal("failed update successful", e)
	}
}
func TestMaintenancePreferenceReplayIsIdempotent(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	for i := 0; i < 2; i++ {
		if e := d.connectMaintenance(context.Background(), controlplane.Job{ID: "p"}, "a", "set_auto_update", map[string]interface{}{"enabled": true}); e != nil {
			t.Fatal(e)
		}
	}
	enabled, _, e := d.maintenanceSnapshot("a")
	if e != nil || !enabled {
		t.Fatal(enabled, e)
	}
}

func TestMaintenanceInterruptedAutoRetryIsDailyAndReverified(t *testing.T) {
	d := maintenanceFixture(t)
	clock := time.Unix(2000000000, 0)
	maintenanceNow = func() time.Time { return clock }
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", AutoUpdate: true, ExpectedVersion: "0.6.0-r38", PreviousVersion: "0.6.0-r37", LastPoll: clock}); e != nil {
		t.Fatal(e)
	}
	polls, installs := 0, 0
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		polls++
		return fakeMaintenanceFeed("own.ipk")
	}
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	maintenanceAutoInstall = func(context.Context, maintenanceCandidate) error {
		installs++
		return errors.New("fake interrupted install")
	}
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	if polls != 0 || installs != 0 {
		t.Fatal("premature retry")
	}
	clock = clock.Add(24 * time.Hour)
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	if polls != 1 || installs != 1 {
		t.Fatal(polls, installs)
	}
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	if polls != 1 || installs != 1 {
		t.Fatal("unbounded retry")
	}
	clock = clock.Add(24 * time.Hour)
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		polls++
		return "", feedverify.Feed{}, nil, errors.New("signature invalid")
	}
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	if installs != 1 {
		t.Fatal("retry without signature")
	}
}
func TestMaintenanceInstalledNewOldRuntimeRestartIsBounded(t *testing.T) {
	d := maintenanceFixture(t)
	clock := maintenanceNow()
	oldRestart := maintenanceAutoRestart
	t.Cleanup(func() { maintenanceAutoRestart = oldRestart })
	oldVersion := runtimeVersion
	t.Cleanup(func() { runtimeVersion = oldVersion })
	runtimeVersion = "0.6.0-r37"
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", ExpectedVersion: "0.6.0-38", PreviousVersion: "0.6.0-37", LastPoll: clock.Add(-24 * time.Hour)}); e != nil {
		t.Fatal(e)
	}
	maintenanceCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte("Version: 0.6.0-38\n"), nil }
	restarts := 0
	maintenanceAutoRestart = func() error { restarts++; return errControllerRestartRequested }
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); !errors.Is(e, errControllerRestartRequested) {
		t.Fatal(e)
	}
	_ = d.connectMaintenanceAfterCheckin(context.Background(), "a")
	if restarts != 1 {
		t.Fatal("restart replay", restarts)
	}
}

func TestMaintenanceCapabilitiesRequireFreshVerifiedOwnerFeed(t *testing.T) {
	d := maintenanceFixture(t)
	clock := maintenanceNow()
	maintenanceNow = func() time.Time { return clock }
	if d.maintenanceFeedAvailable("a") {
		t.Fatal("unobserved capability")
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return fakeMaintenanceFeed("own.ipk")
	}
	maintenanceCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Version: 0.6.0-r38\n"), nil
		}
		t.Fatal("equal version compared")
		return nil, nil
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	if !d.maintenanceFeedAvailable("a") {
		t.Fatal("up-to-date signed observation not supported")
	}
	if d.maintenanceFeedAvailable("b") {
		t.Fatal("previous owner capability")
	}
	clock = clock.Add(-time.Second)
	if d.maintenanceFeedAvailable("a") {
		t.Fatal("future observation")
	}
	clock = clock.Add(24*time.Hour + 2*time.Second)
	if d.maintenanceFeedAvailable("a") {
		t.Fatal("stale observation")
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return "", feedverify.Feed{}, nil, errors.New("signature invalid")
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); e == nil {
		t.Fatal("failed poll hidden")
	}
	if d.maintenanceFeedAvailable("a") {
		t.Fatal("failed signature retained support")
	}
}

func TestMaintenanceRebootSchedulingAcceptedUntilPhysicalProof(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	var mu sync.Mutex
	d := buildGuardTestDaemon(t, results, &mu)
	oldBoot := maintenanceBootID
	t.Cleanup(func() { maintenanceBootID = oldBoot })
	boot := "boot-a"
	maintenanceBootID = func() (string, error) { return boot, nil }
	j := controlplane.Job{ID: "reboot-native"}
	e := d.connectMaintenance(context.Background(), j, "a", "reboot", nil)
	if !errors.Is(e, errMaintenanceActionPending) {
		t.Fatal("scheduled reboot terminal", e)
	}
	r, ok := maintenanceLatestResult(results, &mu, j.ID)
	if !ok || r.Status != "accepted" || r.Result["rebootVerified"] != false {
		t.Fatal(r, ok)
	}
	if !d.maintenancePendingReboot("a", j.ID) {
		t.Fatal("pending receipt lost")
	}
	boot = "boot-b"
	if e = d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	r, ok = maintenanceLatestResult(results, &mu, j.ID)
	if !ok || r.Status != "success" || r.Result["rebootVerified"] != true {
		t.Fatal(r, ok)
	}
	if d.maintenancePendingReboot("a", j.ID) {
		t.Fatal("terminal receipt retained")
	}
}

func maintenanceLatestResult(results map[string][]controlplane.JobResultRequest, mu *sync.Mutex, id string) (controlplane.JobResultRequest, bool) {
	mu.Lock()
	defer mu.Unlock()
	r := results[id]
	if len(r) == 0 {
		return controlplane.JobResultRequest{}, false
	}
	return r[len(r)-1], true
}

func TestMaintenanceTerminalDeliveryKeepsReceiptOnCorruptJournal(t *testing.T) {
	d := maintenanceFixture(t)
	d.st.ClaimOwner = &controlplane.ClaimOwner{OwnerRef: "a"}
	jobID := "reboot-corrupt"
	if e := d.saveMaintenance(maintenanceState{OwnerRef: "a", Reboot: &maintenanceReboot{JobID: jobID, BootID: "boot-old", Acknowledged: true, Dispatched: true, TerminalStatus: "success"}}); e != nil {
		t.Fatal(e)
	}
	path := d.cfg.StatePath + ".connect-actions.json"
	if e := os.WriteFile(path, []byte("broken-json"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), "a"); !errors.Is(e, errMaintenanceActionPending) {
		t.Fatal("journal failure lost pending receipt", e)
	}
	if !d.maintenancePendingReboot("a", jobID) {
		t.Fatal("receipt cleared before journal completion")
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	journal, e := connectactions.OpenJournal(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = journal.Begin(d.connectBinding(), connectactions.Envelope{Origin: "partner_action", ActionID: jobID, OwnerRef: "a", Action: "reboot", Params: map[string]interface{}{}}); e != nil {
		t.Fatal(e)
	}
	if e = d.connectMaintenanceAfterCheckin(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	if d.maintenancePendingReboot("a", jobID) {
		t.Fatal("receipt not cleared after durable journal completion")
	}
}
