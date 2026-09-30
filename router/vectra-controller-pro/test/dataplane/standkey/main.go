// standkey: `standkey new` prints a fresh X25519 private key (base64);
// `standkey claim -state P -private K` writes its public half into the
// router's state.json as check-in's claimKey (kid 1) — the key the router
// seals its tokens to.
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "new" {
		k, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(k.Bytes()))
		return
	}
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	path := fs.String("state", "/etc/vectra-controller-pro/state.json", "")
	private := fs.String("private", "", "")
	_ = fs.Parse(os.Args[2:])
	raw, err := base64.StdEncoding.DecodeString(*private)
	if err != nil {
		log.Fatal(err)
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		log.Fatal(err)
	}
	st, err := state.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	st.ClaimKey = &controlplane.ClaimKey{Kid: 1, PublicKey: base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())}
	if err := state.Save(*path, st); err != nil {
		log.Fatal(err)
	}
}
