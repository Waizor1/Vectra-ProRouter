// wan is the far side of the pilot stand's WAN: every Vectra host a router
// talks to, on one address, behind the names the router resolves to it.
//
//   - DNS on :53 (udp and tcp). vctl and the reporter resolve the control
//     plane's names themselves, over TCP to 8.8.8.8 and 77.88.8.8, never
//     through /etc/hosts (internal/controlplane/resolve.go); the router's
//     nftables send those two to this server. Vectra's names answer with this
//     address, every other name is asked of -upstream (docker's resolver: a
//     DNS reply straight from 8.8.8.8 does not come back on every network).
//   - HTTPS on :443 with a CA made at start (written to -out/ca.pem, which the
//     router adds to its bundle):
//     /install, /artifacts/...   a mirror (-mirror/<-out/feed-<router's
//     address>>, "published" by default: the bytes Vectra published, their
//     own signature included);
//     /api/router/...            a stub panel: register, check-in (jobs from
//     -out/jobs/<router's address>/*.json, then moved to jobs-sent/<address>/),
//     job-result;
//     /errors/router             Connect's bug-report sink, checking every
//     report as the contract says (test/dataplane/reportsink): the body's
//     hash, the token sealed to the stand's key — the claimKey the stub
//     panel names in check-in — and signed by the device key that router
//     registered with. -out/sink-<router's address> holding "down" answers
//     503;
//     /stand/probe?ports=tcp:22,udp:53,...   the stand's own: each port of
//     the asking router's address tried from out here, on its WAN — "open"
//     only for a completed TCP connect or a DNS answer.
//
// Every request is one JSON line in -out/requests.jsonl; nothing a router
// sends as a credential (its token, its User-Agent) is written there.
package main

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vectra-controller-pro/internal/uatoken"
)

var vectraNames = []string{"api.vectra-pro.net", "router.vectra-pro.net", "api-app.vectra-pro.net"}

type stand struct {
	ip       net.IP
	out      string
	mirror   string
	poll     int
	upstream string
	claim    *ecdh.PrivateKey

	mu      sync.Mutex
	routers map[string]*router // by device identifier
	logMu   sync.Mutex
}

// router is what the stub panel keeps of one router: what it registered with.
type router struct {
	RouterID         string `json:"routerId"`
	Token            string `json:"token"`
	DeviceIdentifier string `json:"deviceIdentifier"`
	DevicePublicKey  string `json:"devicePublicKey"`
	Registrations    int    `json:"registrations"`
}

func main() {
	ip := flag.String("ip", "", "this server's address on the stand's network")
	out := flag.String("out", "/out", "control files and logs")
	mirror := flag.String("mirror", "/mirror", "one directory per feed the mirror can serve")
	poll := flag.Int("poll", 5, "the check-in interval the stub panel asks for")
	upstream := flag.String("upstream", "127.0.0.11:53", "where names that are not Vectra's are asked: docker's resolver by default")
	flag.Parse()
	s := &stand{ip: net.ParseIP(*ip).To4(), out: *out, mirror: *mirror, poll: *poll, upstream: *upstream, routers: map[string]*router{}}
	if s.ip == nil {
		log.Fatal("-ip: an IPv4 address")
	}
	var err error
	if s.claim, err = ecdh.X25519().GenerateKey(rand.Reader); err != nil {
		log.Fatal(err)
	}
	for _, d := range []string{"jobs", "jobs-sent"} { // one subdirectory per router in each
		if err := os.MkdirAll(filepath.Join(s.out, d), 0o755); err != nil {
			log.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.out, "routers.json")); err == nil {
		_ = json.Unmarshal(b, &s.routers)
	}
	cert, err := s.makeCert()
	if err != nil {
		log.Fatal(err)
	}
	// Every listener bound before the stand is told the WAN side is up.
	udp, err := net.ListenPacket("udp", ":53")
	if err != nil {
		log.Fatal(err)
	}
	tcp, err := net.Listen("tcp", ":53")
	if err != nil {
		log.Fatal(err)
	}
	web, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	go s.serveDNSUDP(udp)
	go s.serveDNSTCP(tcp)
	srv := &http.Server{
		Handler:           http.HandlerFunc(s.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := os.WriteFile(filepath.Join(s.out, "ready"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Fatal(srv.Serve(tls.NewListener(web, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
}

// record appends one line to requests.jsonl.
func (s *stand) record(kind string, fields map[string]any) {
	fields["t"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["kind"] = kind
	b, _ := json.Marshal(fields)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.out, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func (s *stand) makeCert() (tls.Certificate, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pilot stand CA"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: vectraNames[0]},
		DNSNames:     append([]string{"*.vectra-pro.net"}, vectraNames...),
		IPAddresses:  []net.IP{s.ip},
		NotBefore:    now.Add(-24 * time.Hour),
		NotAfter:     now.Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(filepath.Join(s.out, "ca.pem"), caPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}, nil
}

// ------------------------------------------------------------------ DNS ----

// vectraName: any name under vectra-pro.net answers with the stand, so a
// Vectra host the stand does not know of fails here instead of reaching the
// real one.
func vectraName(q string) bool {
	q = strings.TrimSuffix(strings.ToLower(q), ".")
	return q == "vectra-pro.net" || strings.HasSuffix(q, ".vectra-pro.net")
}

// question reads the first question of a DNS message: its name, its type and
// the offset just past it.
func question(msg []byte) (string, uint16, int, error) {
	if len(msg) < 12 || binary.BigEndian.Uint16(msg[4:6]) < 1 {
		return "", 0, 0, errors.New("no question")
	}
	var labels []string
	i := 12
	for {
		if i >= len(msg) {
			return "", 0, 0, errors.New("short name")
		}
		n := int(msg[i])
		i++
		if n == 0 {
			break
		}
		if n&0xc0 != 0 || i+n > len(msg) {
			return "", 0, 0, errors.New("bad label")
		}
		labels = append(labels, string(msg[i:i+n]))
		i += n
	}
	if i+4 > len(msg) {
		return "", 0, 0, errors.New("short question")
	}
	return strings.Join(labels, "."), binary.BigEndian.Uint16(msg[i : i+2]), i + 4, nil
}

// answer is the reply to a question for one of Vectra's names: this address
// for A, no record for anything else.
func (s *stand) answer(msg []byte, qtype uint16, end int) []byte {
	r := append([]byte(nil), msg[:end]...)
	r[2] = 0x84 | (msg[2] & 0x01) // QR, AA, RD as asked
	r[3] = 0x80                   // RA, NOERROR
	binary.BigEndian.PutUint16(r[4:6], 1)
	binary.BigEndian.PutUint16(r[8:10], 0)
	binary.BigEndian.PutUint16(r[10:12], 0)
	if qtype == 1 {
		binary.BigEndian.PutUint16(r[6:8], 1)
		r = append(r, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4)
		r = append(r, s.ip...)
	} else {
		binary.BigEndian.PutUint16(r[6:8], 0)
	}
	return r
}

func (s *stand) resolve(msg []byte, transport string, from net.Addr) ([]byte, error) {
	name, qtype, end, err := question(msg)
	if err != nil {
		return nil, err
	}
	ours := vectraName(name)
	s.record("dns", map[string]any{"from": from.String(), "via": transport, "name": name, "qtype": qtype, "vectra": ours})
	if ours {
		return s.answer(msg, qtype, end), nil
	}
	r, err := s.forward(msg, transport)
	if err != nil {
		s.record("dns-error", map[string]any{"name": name, "via": transport, "error": err.Error()})
	}
	return r, err
}

func (s *stand) forward(msg []byte, transport string) ([]byte, error) {
	c, err := net.DialTimeout(transport, s.upstream, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if transport == "udp" {
		if _, err := c.Write(msg); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	if err := writeTCPDNS(c, msg); err != nil {
		return nil, err
	}
	return readTCPDNS(c)
}

func readTCPDNS(c net.Conn) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(c, l[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(c, b)
	return b, err
}

func writeTCPDNS(c net.Conn, msg []byte) error {
	b := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(b, uint16(len(msg)))
	copy(b[2:], msg)
	_, err := c.Write(b)
	return err
}

func (s *stand) serveDNSUDP(pc net.PacketConn) {
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		msg := append([]byte(nil), buf[:n]...)
		go func() {
			if r, err := s.resolve(msg, "udp", from); err == nil {
				_, _ = pc.WriteTo(r, from)
			}
		}()
	}
}

func (s *stand) serveDNSTCP(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer c.Close()
			for {
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				msg, err := readTCPDNS(c)
				if err != nil {
					return
				}
				r, err := s.resolve(msg, "tcp", c.RemoteAddr())
				if err != nil || writeTCPDNS(c, r) != nil {
					return
				}
			}
		}()
	}
}

// ----------------------------------------------------------------- HTTP ----

// remoteIP is the address a request came from: each router's own controls.
func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *stand) control(name, fallback string) string {
	b, err := os.ReadFile(filepath.Join(s.out, name))
	if err != nil {
		return fallback
	}
	if v := strings.TrimSpace(string(b)); v != "" {
		return v
	}
	return fallback
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (s *stand) serveHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w}
	fields := map[string]any{"from": r.RemoteAddr, "host": r.Host, "method": r.Method, "path": r.URL.Path}
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/install" || strings.HasPrefix(r.URL.Path, "/artifacts/")):
		s.serveMirror(sw, r, fields)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/router/"):
		s.servePanel(sw, r, fields)
	case r.Method == http.MethodPost && r.URL.Path == "/errors/router":
		s.serveSink(sw, r, fields)
	case r.Method == http.MethodGet && r.URL.Path == "/stand/probe":
		s.serveProbe(sw, r, fields)
	default:
		http.NotFound(sw, r)
	}
	fields["status"] = sw.status
	fields["bytes"] = sw.bytes
	s.record("http", fields)
}

func (s *stand) serveMirror(w http.ResponseWriter, r *http.Request, fields map[string]any) {
	feed := s.control("feed-"+remoteIP(r), "published")
	fields["feed"] = feed
	root := filepath.Join(s.mirror, filepath.Base(feed))
	rel := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if rel == "install" {
		rel = "install.sh"
	}
	p := filepath.Join(root, rel)
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if b, err := os.ReadFile(p); err == nil {
		sum := sha256.Sum256(b)
		fields["sha256"] = hex.EncodeToString(sum[:])
	}
	http.ServeFile(w, r, p)
}

func (s *stand) saveRouters() {
	b, _ := json.MarshalIndent(s.routers, "", "  ")
	_ = os.WriteFile(filepath.Join(s.out, "routers.json"), b, 0o644)
}

func (s *stand) byRouterID(id string) *router {
	for _, rt := range s.routers {
		if rt.RouterID == id {
			return rt
		}
	}
	return nil
}

func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func str(v any) string { s, _ := v.(string); return s }

func (s *stand) claimKey() map[string]any {
	return map[string]any{"kid": 1, "publicKey": base64.StdEncoding.EncodeToString(s.claim.PublicKey().Bytes())}
}

// inventoryFields is what the stand's checks read from a register or check-in.
func inventoryFields(body map[string]any, fields map[string]any) {
	inv, _ := body["inventory"].(map[string]any)
	for _, k := range []string{"deviceIdentifier", "controllerVersion", "controllerRuntimeVersion", "remoteShell", "engineMode", "architecture", "openwrtRelease"} {
		if v, ok := inv[k]; ok {
			fields[k] = v
		}
	}
	if pk := str(inv["devicePublicKey"]); pk != "" {
		sum := sha256.Sum256([]byte(pk))
		fields["devicePublicKeySha"] = hex.EncodeToString(sum[:8])
	}
	for _, k := range []string{"supportAccess", "routerPasswordSet", "autoUpdate"} {
		if v := get(inv, "connect", k); v != nil {
			fields["connect_"+k] = v
		}
	}
	if v := get(inv, "serviceHealth", "xray"); v != nil {
		fields["serviceHealth_xray"] = v
	}
}

func (s *stand) servePanel(w http.ResponseWriter, r *http.Request, fields map[string]any) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", 400)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		fields["error"] = "json"
		http.Error(w, "json", 400)
		return
	}
	inventoryFields(body, fields)
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := func(v map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/api/router/register":
		id := str(get(body, "inventory", "deviceIdentifier"))
		rt := s.routers[id]
		if rt == nil {
			rt = &router{RouterID: fmt.Sprintf("pilot-%02d", len(s.routers)+1), DeviceIdentifier: id}
			s.routers[id] = rt
		}
		rt.Token = fmt.Sprintf("tok-%s-%d", rt.RouterID, rt.Registrations+1)
		rt.DevicePublicKey = str(get(body, "inventory", "devicePublicKey"))
		rt.Registrations++
		s.saveRouters()
		fields["routerId"] = rt.RouterID
		fields["registrations"] = rt.Registrations
		fields["proof"] = body["proof"] != nil
		reply(map[string]any{
			"protocolVersion": body["protocolVersion"], "routerId": rt.RouterID, "status": "approved",
			"issuedToken": rt.Token, "pollingIntervalSeconds": s.poll, "pendingApproval": false,
			"configSyncState": map[string]any{"importState": "idle"}, "operatorMessage": "",
			"claimKey": s.claimKey(),
		})
	case "/api/router/check-in":
		id := r.Header.Get("x-vectra-router-id")
		rt := s.byRouterID(id)
		fields["routerId"] = id
		fields["known"] = rt != nil
		fields["tokenOK"] = rt != nil && r.Header.Get("x-vectra-router-token") == rt.Token
		fields["signed"] = r.Header.Get("x-vectra-checkin-signature") != ""
		if rt == nil || !fields["tokenOK"].(bool) {
			w.WriteHeader(401)
			reply(map[string]any{"error": "unknown router"})
			return
		}
		jobs := s.takeJobs(remoteIP(r), fields)
		reply(map[string]any{
			"protocolVersion": body["protocolVersion"], "routerId": rt.RouterID, "status": "approved",
			"pollingIntervalSeconds": s.poll, "configSyncState": map[string]any{"importState": "idle"},
			"rescuePolicy": map[string]any{}, "updatePolicy": map[string]any{}, "jobs": jobs,
			"operatorMessage": "", "desiredRevision": nil, "claimKey": s.claimKey(),
		})
	case "/api/router/job-result":
		fields["routerId"] = body["routerId"]
		fields["jobId"] = body["jobId"]
		fields["jobStatus"] = body["status"]
		fields["stdout"] = clip(str(body["stdout"]), 600)
		fields["stderr"] = clip(str(body["stderr"]), 600)
		if res, ok := body["result"].(map[string]any); ok {
			b, _ := json.Marshal(res)
			fields["result"] = clip(string(b), 600)
		}
		reply(map[string]any{"protocolVersion": body["protocolVersion"], "acknowledged": true})
	default:
		w.WriteHeader(404)
		reply(map[string]any{"error": "stand panel: no such endpoint"})
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// takeJobs hands out every job queued for this router (-out/jobs/<ip>/*.json) once.
func (s *stand) takeJobs(ip string, fields map[string]any) []any {
	names, _ := filepath.Glob(filepath.Join(s.out, "jobs", ip, "*.json"))
	sort.Strings(names)
	jobs := []any{}
	var ids []string
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		var j map[string]any
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if _, ok := j["createdAt"]; !ok {
			j["createdAt"] = time.Now().UTC().Format(time.RFC3339)
		}
		if _, ok := j["state"]; !ok {
			j["state"] = "queued"
		}
		jobs = append(jobs, j)
		ids = append(ids, str(j["id"]))
		sent := filepath.Join(s.out, "jobs-sent", ip)
		_ = os.MkdirAll(sent, 0o755)
		_ = os.Rename(n, filepath.Join(sent, filepath.Base(n)))
	}
	if len(ids) > 0 {
		fields["jobsSent"] = ids
	}
	return jobs
}

func (s *stand) serveSink(w http.ResponseWriter, r *http.Request, fields map[string]any) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	sum := sha256.Sum256(body)
	note := func(verdict string) {
		fields["sink"] = verdict
		s.logMu.Lock()
		defer s.logMu.Unlock()
		f, err := os.OpenFile(filepath.Join(s.out, "sink.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), verdict)
			f.Close()
		}
	}
	if s.control("sink-"+remoteIP(r), "accept") == "down" {
		note("DOWN 503")
		w.WriteHeader(503)
		return
	}
	if r.URL.Query().Get("b") != hex.EncodeToString(sum[:]) {
		note("BAD body-hash")
		w.WriteHeader(400)
		return
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return s.claim, kid == 1 }
	devices := func(id string) (ed25519.PublicKey, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		rt := s.routers[id]
		if rt == nil {
			return nil, false
		}
		pub, err := base64.StdEncoding.DecodeString(rt.DevicePublicKey)
		return ed25519.PublicKey(pub), err == nil && len(pub) == ed25519.PublicKeySize
	}
	opened, err := uatoken.Open(r.UserAgent(), keys, devices, r.Header.Get("x-hwid"), r.URL.RequestURI(), time.Now(), 5*time.Minute)
	if err != nil {
		note("BAD token " + err.Error())
		w.WriteHeader(401)
		return
	}
	var rep struct {
		Code    string    `json:"code"`
		Count   int       `json:"count"`
		FirstAt time.Time `json:"firstAt"`
		Router  struct {
			Vctl string `json:"vctl"`
		} `json:"router"`
	}
	if json.Unmarshal(body, &rep) != nil {
		note("BAD json")
		w.WriteHeader(422)
		return
	}
	note(fmt.Sprintf("OK %s %d %s vctl=%s first=%d", rep.Code, rep.Count, opened.DeviceID, rep.Router.Vctl, rep.FirstAt.Unix()))
	w.WriteHeader(202)
}

// serveProbe tries each port of the asking router's address from the WAN side.
func (s *stand) serveProbe(w http.ResponseWriter, r *http.Request, fields map[string]any) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "addr", 400)
		return
	}
	out := map[string]string{}
	for _, p := range strings.Split(r.URL.Query().Get("ports"), ",") {
		proto, port, ok := strings.Cut(strings.TrimSpace(p), ":")
		if !ok {
			continue
		}
		addr := net.JoinHostPort(host, port)
		switch proto {
		case "tcp":
			c, err := net.DialTimeout("tcp", addr, 3*time.Second)
			if err == nil {
				c.Close()
				out[p] = "open"
			} else {
				out[p] = "closed"
			}
		case "udp":
			out[p] = "closed"
			c, err := net.DialTimeout("udp", addr, 3*time.Second)
			if err != nil {
				continue
			}
			// A DNS question for localhost: an answer of any kind is a listener.
			q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0, 0, 1, 0, 1}
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 512)
			if _, err := c.Write(q); err == nil {
				if n, err := c.Read(buf); err == nil && n >= 12 {
					out[p] = "open"
				}
			}
			c.Close()
		}
	}
	fields["probe"] = out
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
