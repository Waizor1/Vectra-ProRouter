package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"vectra-controller-pro/internal/subscription"
)

func cmdSubscribe(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("subscribe: subcommand required: fetch | parse | hwid")
	}
	switch args[0] {
	case "fetch":
		return subscribeFetch(args[1:])
	case "parse":
		return subscribeParse(args[1:])
	case "hwid":
		return subscribeHWID(args[1:])
	default:
		return fmt.Errorf("subscribe: unknown subcommand %q", args[0])
	}
}

func subscribeFetch(args []string) error {
	fs := newFlagSet("subscribe fetch")
	url := fs.String("url", "", "subscription URL (required)")
	ua := fs.String("ua", "passwall2/26.5.1", "User-Agent header to send")
	hwid := fs.String("hwid", "", "x-hwid value (precomputed)")
	mac := fs.String("mac", "", "eth0 MAC for HWID computation (if hwid not given)")
	model := fs.String("model", "", "device model (e.g. 'Xiaomi Mi Router AX3000T')")
	osRel := fs.String("os-release", "", "OpenWrt DISTRIB_RELEASE (e.g. '24.10.6')")
	out := fs.String("out", "", "write raw body to file; '-' for stdout, empty = stderr summary only")
	parse := fs.Bool("parse", false, "also parse the body and print a JSON summary")
	timeout := fs.Int("timeout", 30, "max total fetch timeout (seconds)")
	connectTimeout := fs.Int("connect-timeout", 5, "connect timeout (seconds)")
	retries := fs.Int("retries", 2, "number of retries on transient failures")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		fs.Usage()
		return fmt.Errorf("-url is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	res, err := subscription.Fetch(ctx, subscription.FetchOptions{
		URL:            *url,
		UserAgent:      *ua,
		HWID:           *hwid,
		MAC:            *mac,
		Model:          *model,
		OSRelease:      *osRel,
		ConnectTimeout: time.Duration(*connectTimeout) * time.Second,
		MaxTimeout:     time.Duration(*timeout) * time.Second,
		Retries:        *retries,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "HTTP %d %s · %d bytes\n", res.StatusCode, res.ContentType, res.BodyBytes)
	if res.ProfileTitle != "" {
		fmt.Fprintf(os.Stderr, "profile-title: %s\n", res.ProfileTitle)
	}
	if res.UserInfo != nil {
		fmt.Fprintf(os.Stderr, "userinfo: down=%d total=%d expire=%s\n",
			res.UserInfo.DownloadBytes, res.UserInfo.TotalBytes, res.UserInfo.ExpireAt.Format(time.RFC3339))
	}
	if res.ProfileUpdateIntervalDays > 0 {
		fmt.Fprintf(os.Stderr, "profile-update-interval: %d days\n", res.ProfileUpdateIntervalDays)
	}
	switch *out {
	case "":
		// summary only
	case "-":
		_, _ = os.Stdout.Write(res.Body)
	default:
		if err := os.WriteFile(*out, res.Body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *out, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(res.Body), *out)
	}
	if *parse {
		pr := subscription.ParseBody(res.Body)
		summary := struct {
			Format        string `json:"format"`
			Nodes         int    `json:"nodes"`
			Unparsed      int    `json:"unparsed"`
			DecodedBytes  int    `json:"decodedBytes"`
		}{pr.BodyFormat, len(pr.Nodes), len(pr.UnparsedLines), pr.DecodedBytes}
		b, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintln(os.Stderr, string(b))
	}
	return nil
}

func subscribeParse(args []string) error {
	fs := newFlagSet("subscribe parse")
	in := fs.String("in", "", "input file (raw subscription body); '-' for stdin")
	out := fs.String("out", "-", "where to write parsed JSON; '-' for stdout")
	verbose := fs.Bool("v", false, "include UnknownParams and RawURI in output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		fs.Usage()
		return fmt.Errorf("-in is required")
	}
	body, err := readFileOrStdin(*in)
	if err != nil {
		return err
	}
	pr := subscription.ParseBody(body)
	if !*verbose {
		for i := range pr.Nodes {
			pr.Nodes[i].RawURI = ""
			pr.Nodes[i].UnknownParams = nil
		}
	}
	w := os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(pr)
}

func subscribeHWID(args []string) error {
	fs := newFlagSet("subscribe hwid")
	mac := fs.String("mac", "", "eth0 MAC, lowercase colon-separated")
	model := fs.String("model", "", "device model, exact /tmp/sysinfo/model string")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mac == "" || *model == "" {
		fs.Usage()
		return fmt.Errorf("-mac and -model are required")
	}
	fmt.Println(subscription.ComputeHWID(*mac, *model))
	return nil
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return readAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func readAll(f *os.File) ([]byte, error) {
	var sb strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
	}
	return []byte(sb.String()), nil
}
