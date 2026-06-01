package main

import (
	"context"
	"fmt"
	"time"

	"vectra-controller-pro/internal/happcrypt"
)

func init() {
	register(command{
		name:    "happ-crypt",
		summary: "Encrypt a subscription URL into a Happ crypto link (crypt2/3/4 offline, crypt5 via Happ API)",
		run:     cmdHappCrypt,
	})
}

func cmdHappCrypt(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("happ-crypt: subcommand required: encrypt")
	}
	switch args[0] {
	case "encrypt":
		return happCryptEncrypt(args[1:])
	default:
		return fmt.Errorf("happ-crypt: unknown subcommand %q (want: encrypt)", args[0])
	}
}

func happCryptEncrypt(args []string) error {
	fs := newFlagSet("happ-crypt encrypt")
	subURL := fs.String("url", "", "subscription URL to encrypt (required)")
	version := fs.Int("version", 5, "Happ crypt version: 2|3|4 (offline) or 5 (via Happ API)")
	apiURL := fs.String("api-url", happcrypt.DefaultAPIEndpoint, "crypt5 minting API endpoint")
	timeout := fs.Int("timeout", 15, "API timeout in seconds (crypt5 only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *subURL == "" {
		fs.Usage()
		return fmt.Errorf("-url is required")
	}

	switch *version {
	case 2, 3, 4:
		link, err := happcrypt.LinkOffline(*version, *subURL)
		if err != nil {
			return err
		}
		fmt.Println(link)
		return nil
	case 5:
		client := happcrypt.NewAPIClient()
		client.Endpoint = *apiURL
		client.HTTP = happcrypt.NewHTTPClient(time.Duration(*timeout) * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout+5)*time.Second)
		defer cancel()
		link, err := client.EncryptV5(ctx, *subURL)
		if err != nil {
			return err
		}
		fmt.Println(link)
		return nil
	default:
		return fmt.Errorf("happ-crypt: unsupported version %d (want 2, 3, 4, or 5)", *version)
	}
}
