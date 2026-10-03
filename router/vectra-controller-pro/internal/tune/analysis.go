package tune

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"vectra-controller-pro/internal/memguard"
)

// Analysis is where the router's memory and flash go, for the operator: what
// xray could be given and what holds it. It only reads; nothing in it is the
// tune's to change. `vctl tune plan` prints it; the router UI's status does
// not carry it (it walks every process and /root at each call).
type Analysis struct {
	// Memory: what the kernel can still hand out, and the swap.
	MemAvailableMiB int `json:"memAvailableMiB"`
	SwapTotalMiB    int `json:"swapTotalMiB"`
	SwapUsedMiB     int `json:"swapUsedMiB"`
	// TopRSS are the processes holding the most RAM, by name, largest first.
	TopRSS []ProcessRSS `json:"topRss"`
	// OverlayFreeMiB is the flash left for configuration and packages; nil
	// when it could not be read.
	OverlayFreeMiB *int `json:"overlayFreeMiB"`
	// Reclaimable is flash someone left behind that the operator may free —
	// never vctl's to delete: packages downloaded by hand and staging
	// directories under /root.
	Reclaimable []Reclaimable `json:"reclaimable"`
}

// ProcessRSS is one process's resident memory.
type ProcessRSS struct {
	Name   string  `json:"name"`
	PID    int     `json:"pid"`
	RSSMiB float64 `json:"rssMiB"`
}

// Reclaimable is one thing on the flash the operator may remove.
type Reclaimable struct {
	Path string  `json:"path"`
	MiB  float64 `json:"mib"`
	// Kind: package (an .ipk) or staging (a directory named so).
	Kind string `json:"kind"`
}

// topRSSCount is how many processes the analysis names.
const topRSSCount = 5

// Analyze reads where the router's memory and flash go. It never fails:
// what cannot be read is left empty.
func Analyze(env Env) Analysis {
	a := Analysis{TopRSS: []ProcessRSS{}, Reclaimable: []Reclaimable{}}
	if in, err := memguard.ReadFrom(filepath.Join(env.ProcDir, "meminfo")); err == nil {
		a.MemAvailableMiB = int(in.AvailableKB / 1024)
		a.SwapTotalMiB = int(in.SwapTotalKB / 1024)
		if in.SwapTotalKB > in.SwapFreeKB {
			a.SwapUsedMiB = int((in.SwapTotalKB - in.SwapFreeKB) / 1024)
		}
	}
	a.TopRSS = topRSS(env.ProcDir, topRSSCount)
	if env.Overlay != "" {
		var st syscall.Statfs_t
		if syscall.Statfs(env.Overlay, &st) == nil {
			free := int(uint64(st.Bavail) * uint64(st.Bsize) / (1 << 20))
			a.OverlayFreeMiB = &free
		}
	}
	if env.RootDir != "" {
		a.Reclaimable = reclaimable(env.RootDir)
	}
	return a
}

// topRSS reads every process's VmRSS (kernel threads have none).
func topRSS(procDir string, n int) []ProcessRSS {
	out := []ProcessRSS{}
	statuses, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*", "status"))
	for _, path := range statuses {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path)))
		if err != nil {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		p := ProcessRSS{PID: pid}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			switch k {
			case "Name":
				p.Name = strings.TrimSpace(v)
			case "VmRSS":
				if kb, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 64); err == nil {
					p.RSSMiB = float64(int(kb/1024*10+0.5)) / 10
				}
			}
		}
		f.Close()
		if p.RSSMiB > 0 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RSSMiB != out[j].RSSMiB {
			return out[i].RSSMiB > out[j].RSSMiB
		}
		return out[i].PID < out[j].PID
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// reclaimable lists, under root and its directories one level down, the
// .ipk files and the directories whose name says staging, with their size.
// Symlinks are not followed.
func reclaimable(root string) []Reclaimable {
	out := []Reclaimable{}
	add := func(path string, d os.DirEntry) {
		switch {
		case d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(d.Name()), ".ipk"):
			if info, err := d.Info(); err == nil {
				out = append(out, Reclaimable{Path: path, MiB: roundMiB(info.Size()), Kind: "package"})
			}
		case d.IsDir() && strings.Contains(strings.ToLower(d.Name()), "staging"):
			out = append(out, Reclaimable{Path: path, MiB: roundMiB(dirBytes(path)), Kind: "staging"})
		}
	}
	top, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, d := range top {
		path := filepath.Join(root, d.Name())
		add(path, d)
		if !d.IsDir() || strings.Contains(strings.ToLower(d.Name()), "staging") {
			continue
		}
		sub, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, s := range sub {
			if s.Type().IsRegular() && strings.HasSuffix(strings.ToLower(s.Name()), ".ipk") {
				add(filepath.Join(path, s.Name()), s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// dirBytes is the size of the regular files under dir; symlinks are not
// followed.
func dirBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func roundMiB(b int64) float64 { return float64(int(float64(b)/(1<<20)*10+0.5)) / 10 }
