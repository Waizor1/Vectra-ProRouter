package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type installSession struct {
	id         string
	token      string
	targetIP   string
	mu         sync.Mutex
	backlog    []installEvent
	subs       map[chan installEvent]struct{}
	terminated bool
}

func newInstallSession(token string, targetIP string) *installSession {
	return &installSession{
		id:       randomToken(12),
		token:    token,
		targetIP: targetIP,
		backlog:  make([]installEvent, 0, 64),
		subs:     make(map[chan installEvent]struct{}),
	}
}

func (s *installSession) response() installResponse {
	return installResponse{SessionID: s.id}
}

func (s *installSession) emit(event installEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.backlog = append(s.backlog, event)
	for ch := range s.subs {
		select {
		case ch <- event:
		default:
		}
	}

	if event.State == "failure" || (event.Stage == "completed" && event.State == "success") {
		s.terminated = true
	}
}

func (s *installSession) emitStage(stage string, state string, message string, code string, checklist []checklistItem) {
	s.emit(installEvent{
		Stage:          stage,
		State:          state,
		Message:        message,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Code:           code,
		ChecklistDelta: checklist,
	})
}

func (s *installSession) emitLog(stage string, chunk string) {
	s.emit(installEvent{
		Stage:            stage,
		State:            "running",
		Message:          fmt.Sprintf("Лог %s", stage),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		CopyableLogChunk: chunk,
	})
}

func (s *installSession) snapshot() []installEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	copyOf := make([]installEvent, len(s.backlog))
	copy(copyOf, s.backlog)
	return copyOf
}

func (s *installSession) subscribe() chan installEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan installEvent, 16)
	s.subs[ch] = struct{}{}
	return ch
}

func (s *installSession) unsubscribe(ch chan installEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.subs, ch)
	close(ch)
}

func (s *installSession) isAuthorized(token string) bool {
	return s.token == token
}

func (s *installSession) writeSSEBacklog(write func([]byte) error) error {
	for _, entry := range s.snapshot() {
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if err := write([]byte("data: ")); err != nil {
			return err
		}
		if err := write(payload); err != nil {
			return err
		}
		if err := write([]byte("\n\n")); err != nil {
			return err
		}
	}
	return nil
}

type installSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*installSession
}

func newInstallSessionStore() *installSessionStore {
	return &installSessionStore{
		sessions: make(map[string]*installSession),
	}
}

func (s *installSessionStore) create(token string, targetIP string) *installSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := newInstallSession(token, targetIP)
	s.sessions[session.id] = session
	return session
}

func (s *installSessionStore) get(id string) *installSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sessions[id]
}

func randomToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

type sessionTokenStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newSessionTokenStore() *sessionTokenStore {
	return &sessionTokenStore{
		tokens: make(map[string]time.Time),
	}
}

func (s *sessionTokenStore) issue() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	token := randomToken(18)
	s.tokens[token] = time.Now().Add(30 * time.Minute)
	return token
}

func (s *sessionTokenStore) isValid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, ok := s.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(expiresAt) {
		delete(s.tokens, token)
		return false
	}
	return true
}
