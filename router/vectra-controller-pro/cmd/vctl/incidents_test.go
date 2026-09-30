package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/incident"
)

func TestApplyRefusedIsAnIncidentButLowMemoryIsNot(t *testing.T) {
	d := &daemon{incidents: incident.NewRecorder(t.TempDir(), time.Minute)}
	refused := fmt.Errorf("apply: refusing to install: %w: %w", apply.ErrRefused, errors.New("xray: failed to load geosite: RUSSIA-OUTSIDE"))
	lowMem := fmt.Errorf("apply: refusing to install: %w: %w", apply.ErrRefused, xray.ErrLowMemory)
	if !d.noteApplyErr(refused) {
		t.Fatal("a refused config was not said")
	}
	if d.noteApplyErr(lowMem) || d.noteApplyErr(errors.New("apply: write xray.json: no space")) {
		t.Fatal("not a refusal, yet said")
	}
}

func TestXrayExitsAreSaidAndAStormToo(t *testing.T) {
	dir := t.TempDir()
	d := &daemon{incidents: incident.NewRecorder(dir, 0)}
	now := time.Unix(1790000000, 0)
	for i := 0; i < 5; i++ {
		d.noteXrayExit(2, errors.New("exit status 2"), 3*time.Second, now.Add(time.Duration(i)*time.Minute))
	}
	codes := map[string]int{}
	for _, p := range incident.Read(dir) {
		codes[p.Code]++
	}
	if codes["XRAY_CRASH"] == 0 || codes["XRAY_RESTART_STORM"] != 1 {
		t.Fatalf("%v", codes)
	}
}

// An xray crash carries xray's own last lines: why it went.
func TestXrayCrashCarriesXraysLastLines(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "xray.log")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(log, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := xrayLogPath
	xrayLogPath = log
	t.Cleanup(func() { xrayLogPath = old })
	inbox := t.TempDir()
	d := &daemon{incidents: incident.NewRecorder(inbox, 0)}
	d.noteXrayExit(1, errors.New("exit status 1"), time.Second, time.Now())
	got := incident.Read(inbox)
	if len(got) != 1 || len(got[0].Log) != 20 || got[0].Log[19] != "line 30" || got[0].Log[0] != "line 11" {
		t.Fatalf("%+v", got)
	}
}
