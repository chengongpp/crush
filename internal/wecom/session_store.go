package wecom

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type sessionStore struct {
	path         string
	mu           sync.Mutex
	ChatSessions map[string]string `json:"chat_sessions"`
}

func loadSessionStore(path string) (*sessionStore, error) {
	store := &sessionStore{
		path:         path,
		ChatSessions: make(map[string]string),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, store); err != nil {
		return nil, err
	}
	if store.ChatSessions == nil {
		store.ChatSessions = make(map[string]string)
	}
	store.path = path
	return store, nil
}

func (s *sessionStore) sessionID(chatKey string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID, ok := s.ChatSessions[chatKey]
	return sessionID, ok
}

func (s *sessionStore) setSessionID(chatKey, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ChatSessions[chatKey] = sessionID
	return s.persistLocked()
}

func (s *sessionStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
