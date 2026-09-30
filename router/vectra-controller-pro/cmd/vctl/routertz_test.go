package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePosixTZ(t *testing.T) {
	for tz, want := range map[string]int{
		"MSK-3":                       3 * 3600,
		"<+03>-3":                     3 * 3600,
		"UTC0":                        0,
		"GMT0":                        0,
		"CET-1CEST,M3.5.0,M10.5.0/3":  3600,
		"EST5EDT,M3.2.0,M11.1.0":      -5 * 3600,
		"<+0530>-5:30":                5*3600 + 30*60,
		"IST-5:30":                    5*3600 + 30*60,
		"<-03>3":                      -3 * 3600,
		"NZST-12NZDT,M9.5.0,M4.1.0/3": 12 * 3600,
	} {
		loc, ok := parsePosixTZ(tz)
		if !ok {
			t.Errorf("%q: not parsed", tz)
			continue
		}
		if _, off := time.Date(2026, 1, 1, 0, 0, 0, 0, loc).Zone(); off != want {
			t.Errorf("%q: offset %d, want %d", tz, off, want)
		}
	}
	for _, bad := range []string{"", "MSK", "-3", "<+03", "MSKx", "MSK-99", "MSK-3:75"} {
		if _, ok := parsePosixTZ(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRouterLocationReadsTheRoutersTZ(t *testing.T) {
	dir := t.TempDir()
	old := tzPaths
	t.Cleanup(func() { tzPaths = old })
	tzPaths = []string{filepath.Join(dir, "none"), filepath.Join(dir, "TZ")}
	if routerLocation() != time.UTC {
		t.Fatal("no TZ file: want UTC")
	}
	if err := os.WriteFile(filepath.Join(dir, "TZ"), []byte("MSK-3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loc := routerLocation()
	// PassWall's "0:00" is midnight in Moscow: 21:00 UTC the day before.
	mid := time.Date(2026, 9, 29, 0, 0, 0, 0, loc)
	if got := mid.UTC(); got.Hour() != 21 || got.Day() != 28 {
		t.Fatalf("midnight MSK = %v UTC", got)
	}
}
