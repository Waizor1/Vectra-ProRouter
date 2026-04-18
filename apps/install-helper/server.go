package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type helperServer struct {
	listenAddr     string
	allowedOrigins map[string]struct{}
	state          *helperStateStore
	sessions       *installSessionStore
	sessionTokens  *sessionTokenStore
	resolver       gatewayResolver
	fingerprinter  hostFingerprinter
	installer      remoteInstaller
}

func newHelperServer() (*helperServer, error) {
	dataDir, err := defaultDataDir()
	if err != nil {
		return nil, err
	}
	state, err := newHelperStateStore(dataDir, keyringSecretStore{})
	if err != nil {
		return nil, err
	}

	server := &helperServer{
		listenAddr:     readEnv("VECTRA_INSTALL_HELPER_ADDR", defaultListenAddr),
		allowedOrigins: parseAllowedOrigins(readEnv("VECTRA_INSTALL_HELPER_ALLOWED_ORIGINS", defaultOrigins)),
		state:          state,
		sessions:       newInstallSessionStore(),
		sessionTokens:  newSessionTokenStore(),
		resolver:       systemGatewayResolver{},
		fingerprinter:  sshHostFingerprinter{},
	}
	server.installer = &helperInstaller{
		state:         state,
		fingerprinter: server.fingerprinter,
	}
	return server, nil
}

func defaultDataDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "vectra-install-helper"), nil
}

func parseAllowedOrigins(value string) map[string]struct{} {
	origins := make(map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		origins[trimmed] = struct{}{}
	}
	return origins
}

func readEnv(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (s *helperServer) serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/scan", s.handleScan)
	mux.HandleFunc("/install", s.handleInstall)
	mux.HandleFunc("/events/", s.handleEvents)

	server := &http.Server{
		Addr:    s.listenAddr,
		Handler: mux,
	}

	fmt.Printf("Vectra install helper listening on http://%s\n", s.listenAddr)
	return server.ListenAndServe()
}

func (s *helperServer) authorizeRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if _, ok := s.allowedOrigins[origin]; !ok {
		http.Error(w, "Origin is not allowed.", http.StatusForbidden)
		return "", false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Headers", "content-type, x-vectra-install-session")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	return origin, true
}

func (s *helperServer) handleOptions(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodOptions {
		return false
	}
	if _, ok := s.authorizeRequest(w, r); !ok {
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *helperServer) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *helperServer) sessionTokenFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Vectra-Install-Session"))
}

func (s *helperServer) requireIssuedToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := s.sessionTokenFromRequest(r)
	if token == "" || !s.sessionTokens.isValid(token) {
		http.Error(w, "Missing or invalid per-tab session token.", http.StatusUnauthorized)
		return "", false
	}
	return token, true
}

func (s *helperServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.handleOptions(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.authorizeRequest(w, r); !ok {
		return
	}

	s.writeJSON(w, http.StatusOK, healthResponse{
		Service:      helperServiceName,
		Version:      helperVersion,
		SessionToken: s.sessionTokens.issue(),
		Capabilities: helperCapabilities{
			Scan:          true,
			Install:       true,
			Events:        true,
			SecureStorage: true,
		},
		SavedCredentialProfiles: s.state.listProfiles(),
	})
}

func (s *helperServer) handleScan(w http.ResponseWriter, r *http.Request) {
	if s.handleOptions(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.authorizeRequest(w, r); !ok {
		return
	}
	if _, ok := s.requireIssuedToken(w, r); !ok {
		return
	}

	response := discoverCandidates(s.resolver, s.fingerprinter, s.state)
	s.writeJSON(w, http.StatusOK, response)
}

func (s *helperServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if s.handleOptions(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	origin, ok := s.authorizeRequest(w, r)
	if !ok {
		return
	}

	sessionToken, ok := s.requireIssuedToken(w, r)
	if !ok {
		return
	}

	var request installRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Request body must be valid JSON.", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.TargetIP) == "" {
		http.Error(w, "targetIp is required.", http.StatusBadRequest)
		return
	}
	if request.Password != "" && request.CredentialProfileID != "" {
		http.Error(w, "password and credentialProfileId are mutually exclusive.", http.StatusBadRequest)
		return
	}

	session := s.sessions.create(sessionToken, request.TargetIP)
	go s.installer.RunInstall(session, origin, request)
	s.writeJSON(w, http.StatusAccepted, session.response())
}

func (s *helperServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.handleOptions(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.authorizeRequest(w, r); !ok {
		return
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/events/")
	session := s.sessions.get(sessionID)
	if session == nil {
		http.Error(w, "Install session was not found.", http.StatusNotFound)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" || !session.isAuthorized(token) {
		http.Error(w, "Session token is invalid.", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is not supported.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")

	writeChunk := func(chunk []byte) error {
		_, err := w.Write(chunk)
		if err == nil {
			flusher.Flush()
		}
		return err
	}
	if err := session.writeSSEBacklog(writeChunk); err != nil {
		return
	}

	ch := session.subscribe()
	defer session.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			payload, err := json.Marshal(event)
			if err != nil {
				return
			}
			if err := writeChunk([]byte("data: ")); err != nil {
				return
			}
			if err := writeChunk(payload); err != nil {
				return
			}
			if err := writeChunk([]byte("\n\n")); err != nil {
				return
			}
		}
	}
}
