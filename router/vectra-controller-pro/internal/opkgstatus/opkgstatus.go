// Package opkgstatus reads opkg's status file (/usr/lib/opkg/status): what is
// installed, at which version. Read here rather than asked of opkg, which
// takes its lock and loads every feed list to say what this file says.
package opkgstatus

import (
	"regexp"
	"strings"
)

// File is opkg's status file on a router.
const File = "/usr/lib/opkg/status"

// Version is the installed version of package name, "" when it is not
// installed (no stanza, or one whose state is not installed).
func Version(status []byte, name string) string {
	pkg, version, installed := "", "", false
	for _, line := range strings.Split(string(status)+"\n", "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			if pkg == name && installed {
				return version
			}
			pkg, version, installed = "", "", false
		case strings.HasPrefix(line, "Package: "):
			pkg = strings.TrimPrefix(line, "Package: ")
		case strings.HasPrefix(line, "Version: "):
			version = strings.TrimPrefix(line, "Version: ")
		case strings.HasPrefix(line, "Status: "):
			installed = strings.HasSuffix(line, " installed")
		}
	}
	return ""
}

var release = regexp.MustCompile(`-r?[0-9]+$`)

// Upstream is a package version without its OpenWrt release: 26.10.21-r1
// and 26.10.21-1 are xray 26.10.21.
func Upstream(v string) string { return release.ReplaceAllString(v, "") }

// AtLeast compares two versions as the installer and the init script do
// (version_ge): the runs of digits, numerically, one by one — 26.9.10-1 is
// newer than 26.9.9-2, and 26.10.21 than 26.9.8.
func AtLeast(v, min string) bool {
	a, b := digitRuns.FindAllString(v, -1), digitRuns.FindAllString(min, -1)
	for i := 0; i < len(a) || i < len(b); i++ {
		p, q := part(a, i), part(b, i)
		if p != q {
			return p > q
		}
	}
	return true
}

var digitRuns = regexp.MustCompile(`[0-9]+`)

func part(runs []string, i int) int {
	if i >= len(runs) {
		return 0
	}
	n := 0
	for _, c := range runs[i] {
		if n > 1e8 {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
