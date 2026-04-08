package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu         sync.RWMutex
	path       string
	state      AppState
	defaultTTL int
}

func NewStore(path string, defaultRefreshHours int) (*Store, error) {
	store := &Store{
		path:       path,
		state:      DefaultState(defaultRefreshHours),
		defaultTTL: defaultRefreshHours,
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.saveLocked()
		}
		return err
	}

	if len(data) == 0 {
		return s.saveLocked()
	}

	var loaded AppState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	if loaded.Subscription.UserAgent == "" {
		loaded.Subscription.UserAgent = "v2raytun"
	}
	if loaded.Subscription.RefreshIntervalHours <= 0 {
		loaded.Subscription.RefreshIntervalHours = s.defaultTTL
	}
	if loaded.Runtime.State == "" {
		loaded.Runtime.State = "stopped"
	}
	if loaded.Tunnels == nil {
		loaded.Tunnels = []TunnelProfile{}
	}

	s.state = loaded
	return nil
}

func (s *Store) Snapshot() AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneState(s.state)
}

func (s *Store) Update(fn func(*AppState) error) (AppState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneState(s.state)
	if err := fn(&next); err != nil {
		return AppState{}, err
	}

	s.state = next
	if err := s.saveLocked(); err != nil {
		return AppState{}, err
	}

	return cloneState(s.state), nil
}

func (s *Store) Replace(next AppState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = cloneState(next)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

func cloneState(in AppState) AppState {
	out := in
	out.Tunnels = make([]TunnelProfile, len(in.Tunnels))
	copy(out.Tunnels, in.Tunnels)
	for i := range out.Tunnels {
		if in.Tunnels[i].ALPN != nil {
			out.Tunnels[i].ALPN = append([]string{}, in.Tunnels[i].ALPN...)
		}
	}
	return out
}
