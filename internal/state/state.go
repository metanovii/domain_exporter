// Package state keeps the result of the last check of each domain and can
// persist it to a file, so a restart does not trigger a lookup of every domain.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is the result of the checks of one domain.
type Entry struct {
	// Expiry is the expiration time from the last successful check.
	Expiry time.Time `json:"expiry"`
	// LastSuccess is when Expiry was obtained. Zero if never succeeded.
	LastSuccess time.Time `json:"last_success"`
	// LastAttempt is when the last check (successful or not) finished.
	LastAttempt time.Time `json:"last_attempt"`
	// LastError is the error of the last check, empty if it succeeded.
	LastError string `json:"last_error,omitempty"`
	// Duration is how long the last check took.
	Duration time.Duration `json:"duration"`
	// NextCheck is when the domain is due for the next check.
	NextCheck time.Time `json:"next_check"`
}

// Valid reports whether the entry holds an expiry obtained less than ttl ago.
func (e Entry) Valid(now time.Time, ttl time.Duration) bool {
	return !e.LastSuccess.IsZero() && now.Sub(e.LastSuccess) < ttl
}

// Store is a concurrency-safe set of entries, optionally backed by a file.
type Store struct {
	mu      sync.RWMutex
	path    string
	entries map[string]Entry
}

// New returns a store. If path is not empty, entries are loaded from it
// (a missing file is not an error) and every Set writes the file back.
func New(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return nil, fmt.Errorf("failed to parse state file %s: %w", path, err)
	}
	return s, nil
}

// Key returns the store key for a domain and its whois host.
func Key(domain, host string) string {
	if host == "" {
		return domain
	}
	return domain + "@" + host
}

// Get returns the entry for key.
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key]
	return e, ok
}

// Set stores the entry for key and, if the store has a file, saves it.
func (s *Store) Set(key string, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
	return s.save()
}

// save writes all entries to a temporary file and renames it over the state
// file, so a crash never leaves a half-written file. Caller holds s.mu.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create state file: %w", err)
	}
	// After a successful rename the temporary file is gone; the error is
	// only meaningful when something failed before, and then it is secondary.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("failed to replace state file: %w", err)
	}
	return nil
}

// Prune removes the entries whose key is not in keep and saves the store.
func (s *Store) Prune(keep []string) error {
	wanted := make(map[string]bool, len(keep))
	for _, k := range keep {
		wanted[k] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.entries {
		if !wanted[k] {
			delete(s.entries, k)
		}
	}
	return s.save()
}
