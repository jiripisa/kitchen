// Package recents persists the most-recently-used namespace and deployment
// selections so subsequent runs can put them at the top of the pickers.
//
// State lives in ${XDG_STATE_HOME:-~/.local/state}/kitchen/recents.json. The
// file is created atomically (write to *.tmp + rename) so a crash between
// runs can't leave a half-written JSON behind.
//
// All entries are scoped by kubeconfig context — the recents you used in
// `prod-eks` don't pollute `dev`.
package recents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// MaxEntries caps the history depth per list.
const MaxEntries = 5

// Store holds the on-disk recents. The zero value is not usable — call Open.
type Store struct {
	path string
	mu   sync.Mutex
	data data
}

type data struct {
	Contexts map[string]*contextRecents `json:"contexts,omitempty"`
}

type contextRecents struct {
	Namespaces  []string            `json:"namespaces,omitempty"`
	Deployments map[string][]string `json:"deployments,omitempty"`
}

// Open loads the store from disk. A missing file is not an error — Open
// returns an empty store, ready to record selections.
func Open() (*Store, error) {
	p, err := storePath()
	if err != nil {
		return nil, err
	}
	s := &Store{path: p, data: data{Contexts: map[string]*contextRecents{}}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Empty returns an in-memory store that never persists. Useful as a fallback
// when Open fails for a reason we don't want to abort startup over.
func Empty() *Store {
	return &Store{data: data{Contexts: map[string]*contextRecents{}}}
}

func storePath() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "kitchen", "recents.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home: %w", err)
	}
	return filepath.Join(home, ".local", "state", "kitchen", "recents.json"), nil
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recents: %w", err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return fmt.Errorf("parse recents: %w", err)
	}
	if s.data.Contexts == nil {
		s.data.Contexts = map[string]*contextRecents{}
	}
	return nil
}

// Namespaces returns the most-recently-used namespace names for a context,
// newest first. Returns nil for unknown contexts.
func (s *Store) Namespaces(context string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Contexts[context]
	if !ok {
		return nil
	}
	return append([]string(nil), c.Namespaces...)
}

// Deployments returns the most-recently-used deployment names for a (context,
// namespace) pair, newest first.
func (s *Store) Deployments(context, namespace string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Contexts[context]
	if !ok || c.Deployments == nil {
		return nil
	}
	return append([]string(nil), c.Deployments[namespace]...)
}

// RecordNamespace bumps a namespace to the front of the recents list for the
// given context and persists the change. A no-op when the store is in-memory.
func (s *Store) RecordNamespace(context, namespace string) error {
	if context == "" || namespace == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contextLocked(context)
	c.Namespaces = pushFront(c.Namespaces, namespace, MaxEntries)
	return s.saveLocked()
}

// RecordDeployment bumps a deployment to the front of the recents list for the
// given (context, namespace) pair.
func (s *Store) RecordDeployment(context, namespace, deployment string) error {
	if context == "" || namespace == "" || deployment == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contextLocked(context)
	if c.Deployments == nil {
		c.Deployments = map[string][]string{}
	}
	c.Deployments[namespace] = pushFront(c.Deployments[namespace], deployment, MaxEntries)
	return s.saveLocked()
}

func (s *Store) contextLocked(name string) *contextRecents {
	c, ok := s.data.Contexts[name]
	if !ok {
		c = &contextRecents{}
		s.data.Contexts[name] = c
	}
	return c
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil // in-memory store, nothing to do
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write recents: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install recents: %w", err)
	}
	return nil
}

// pushFront prepends item to list, drops any earlier occurrence so the list
// has no duplicates, and truncates to max entries.
func pushFront(list []string, item string, max int) []string {
	out := make([]string, 0, len(list)+1)
	out = append(out, item)
	for _, x := range list {
		if x == item {
			continue
		}
		out = append(out, x)
		if len(out) == max {
			break
		}
	}
	return out
}
