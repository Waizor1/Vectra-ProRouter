package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRetirementFinishRetriesDocumentCleanup(t *testing.T) {
	for _, source := range []string{"", "native", "passwall"} {
		t.Run("source_"+source, func(t *testing.T) {
			r := newHandRemovedRouter(t)
			r.d.cfg.RouteSource = source
			res, err := r.env.Tidy(r0)
			if err != nil || res.Cleanup != nil {
				t.Fatalf("tidy %v %+v", err, res)
			}
			path := passwallDocumentPath(r.d.cfg.ProviderConfigPath)
			if source == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				r.write(filepath.Join(path, "blocked"), "data", 0600)
				r.d.maybeRetirePassWall(context.Background(), r0)
				if err := os.Remove(filepath.Join(path, "blocked")); err != nil {
					t.Fatal(err)
				}
			}
			r.d.maybeRetirePassWall(context.Background(), r0)
			if got := fileExists(path); got != (source != "") {
				t.Fatalf("document remains %v for source %q", got, source)
			}
		})
	}
}
func TestRetirementFinishKeepsDocumentWhenPassWallReturns(t *testing.T) {
	r := newHandRemovedRouter(t)
	if _, err := r.env.Tidy(r0); err != nil {
		t.Fatal(err)
	}
	r.putBack()
	r.d.retireNextAt = r0.Add(retireCheckEvery)
	r.d.maybeRetirePassWall(context.Background(), r0)
	if !fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) {
		t.Fatal("document deleted with PassWall present")
	}
}

func TestRetirementFinishKeepsDocumentWithoutPersistedBackup(t *testing.T) {
	r := newHandRemovedRouter(t)
	res, err := r.env.Tidy(r0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(res.Backup); err != nil {
		t.Fatal(err)
	}
	r.d.maybeRetirePassWall(context.Background(), r0)
	if !fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) {
		t.Fatal("document deleted without persisted backup")
	}
}
