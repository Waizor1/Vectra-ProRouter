// reportsink is the stand's Vectra Connect for bug reports: HTTPS on -listen
// with the stand's certificate, every POST checked as the contract says (the
// body's hash, the token for this path, this HWID, this device) and one line
// per report appended to -log: "OK <code> <count>" or "BAD <reason>".
package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:443", "")
	cert := flag.String("cert", "", "")
	key := flag.String("key", "", "")
	vectraKey := flag.String("vectra-key", "", "Vectra's X25519 private key, base64 (kid 1)")
	statePath := flag.String("state", "/etc/vectra-controller-pro/state.json", "the router's state.json: its device key")
	out := flag.String("log", "/tmp/reportsink.log", "")
	flag.Parse()
	raw, err := base64.StdEncoding.DecodeString(*vectraKey)
	if err != nil {
		log.Fatal(err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		log.Fatal(err)
	}
	note := func(format string, a ...any) {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, format+"\n", a...)
			f.Close()
		}
	}
	http.HandleFunc("/errors/router", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		sum := sha256.Sum256(body)
		if r.URL.Query().Get("b") != hex.EncodeToString(sum[:]) {
			note("BAD body-hash")
			w.WriteHeader(400)
			return
		}
		st, err := state.Load(*statePath)
		if err != nil {
			note("BAD state %v", err)
			w.WriteHeader(503)
			return
		}
		pub, _ := base64.StdEncoding.DecodeString(st.DevicePublicKey)
		keys := func(kid byte) (*ecdh.PrivateKey, bool) { return priv, kid == 1 }
		devices := func(id string) (ed25519.PublicKey, bool) { return ed25519.PublicKey(pub), id == st.DeviceIdentifier }
		if _, err := uatoken.Open(r.UserAgent(), keys, devices, r.Header.Get("x-hwid"), r.URL.RequestURI(), time.Now(), 5*time.Minute); err != nil {
			note("BAD token %v", err)
			w.WriteHeader(401)
			return
		}
		var rep struct {
			Code  string `json:"code"`
			Count int    `json:"count"`
		}
		if json.Unmarshal(body, &rep) != nil {
			note("BAD json")
			w.WriteHeader(422)
			return
		}
		note("OK %s %d", rep.Code, rep.Count)
		w.WriteHeader(202)
	})
	log.Fatal(http.ListenAndServeTLS(*listen, *cert, *key, nil))
}
