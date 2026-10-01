// tlsproxy gives synthetic stand panels HTTPS without relaxing vctl's URL guard.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	listen := flag.String("listen", "", "TLS listen address")
	upstream := flag.String("upstream", "", "stand-local HTTP panel")
	cert := flag.String("cert", "/stand/tls/cert.pem", "stand certificate")
	key := flag.String("key", "/stand/tls/key.pem", "stand key")
	flag.Parse()
	target, err := url.Parse(*upstream)
	if err != nil || target.Scheme != "http" || target.Host == "" || *listen == "" {
		log.Fatal("invalid stand proxy addresses")
	}
	server := &http.Server{Addr: *listen, Handler: httputil.NewSingleHostReverseProxy(target),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	log.Fatal(server.ListenAndServeTLS(*cert, *key))
}
