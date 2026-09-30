package openwrt

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The data-plane stand copies files/ wholesale into its package payload, so a
// file the Makefile forgets to install still works on the stand — and is then
// missing from every real package. This is the check the stand cannot make.
func TestMakefileInstallsExactlyTheShippedFiles(t *testing.T) {
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	installed := map[string]bool{}
	for _, m := range regexp.MustCompile(`\./files/(\S+)`).FindAllStringSubmatch(string(mk), -1) {
		installed[m[1]] = true
	}
	shipped := map[string]bool{}
	err = filepath.WalkDir("files", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("files", p)
		shipped[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing, phantom []string
	for f := range shipped {
		if !installed[f] {
			missing = append(missing, f)
		}
	}
	for f := range installed {
		if !shipped[f] {
			phantom = append(phantom, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(phantom)
	if len(missing) > 0 {
		t.Errorf("in files/ but never installed by the Makefile: %v", missing)
	}
	if len(phantom) > 0 {
		t.Errorf("installed by the Makefile but absent from files/: %v", phantom)
	}
}

// rpcd rejects an ACL or menu file it cannot parse and says nothing about it;
// LuCI then simply has no Vectra page.
func TestRouterUIRegistrationFilesAreValid(t *testing.T) {
	var acl map[string]struct {
		Read  struct{ Ubus map[string][]string } `json:"read"`
		Write struct{ Ubus map[string][]string } `json:"write"`
	}
	mustJSON(t, "files/usr/share/rpcd/acl.d/vectra-controller-pro.json", &acl)
	g, ok := acl["vectra-controller-pro"]
	if !ok || len(g.Read.Ubus["vectra"]) == 0 || len(g.Write.Ubus["vectra"]) == 0 {
		t.Fatalf("acl = %+v", acl)
	}

	var menu map[string]struct {
		Order  int `json:"order"`
		Action struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"action"`
		Depends struct {
			ACL []string `json:"acl"`
		} `json:"depends"`
	}
	mustJSON(t, "files/usr/share/luci/menu.d/luci-app-vectra-controller-pro.json", &menu)
	// A top-level entry ordered before Status (10): LuCI's `admin` node opens its
	// first child, so the router's owner lands on Vectra right after logging in.
	entry, ok := menu["admin/vectra"]
	if !ok || entry.Action.Type != "view" {
		t.Fatalf("menu = %+v", menu)
	}
	if entry.Order <= 0 || entry.Order >= 10 {
		t.Errorf("menu order %d: the page must come before Status (10) to be the landing page", entry.Order)
	}
	view := filepath.Join("files/www/luci-static/resources/view", entry.Action.Path+".js")
	if _, err := os.Stat(view); err != nil {
		t.Errorf("the menu points at %s, which does not ship: %v", view, err)
	}
	if len(entry.Depends.ACL) != 1 || entry.Depends.ACL[0] != "vectra-controller-pro" {
		t.Errorf("menu depends on %v, not the package's ACL group", entry.Depends.ACL)
	}

	plugin, err := os.ReadFile("files/usr/libexec/rpcd/vectra")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(plugin), "#!/bin/sh") || !strings.Contains(string(plugin), "exec /usr/sbin/vctl rpcd") {
		t.Errorf("rpcd plugin does not exec vctl rpcd:\n%s", plugin)
	}
	if st, _ := os.Stat("files/usr/libexec/rpcd/vectra"); st == nil || st.Mode().Perm()&0o111 == 0 {
		t.Error("the rpcd plugin is not executable; rpcd ignores non-executable plugins")
	}
}

func mustJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
