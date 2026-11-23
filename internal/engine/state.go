// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
)

// State is persisted to state.json so learned values survive restarts and
// stay available offline.
type State struct {
	path string
	mu   sync.Mutex
	data stateData
	dirt bool
}

type stateData struct {
	DriftPPM     *float64           `json:"drift_ppm,omitempty"`
	LastSync     time.Time          `json:"last_sync,omitempty"`
	LastOffsetMs float64            `json:"last_offset_ms,omitempty"`
	Learned      map[string]float64 `json:"learned,omitempty"`
}

// LoadState reads path (a missing or corrupt file yields an empty state).
func LoadState(path string) *State {
	s := &State{path: path, data: stateData{Learned: map[string]float64{}}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
		if s.data.Learned == nil {
			s.data.Learned = map[string]float64{}
		}
	}
	return s
}

// GetFloat implements sources.Store.
func (s *State) GetFloat(key string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Learned[key]
	return v, ok
}

// SetFloat implements sources.Store.
func (s *State) SetFloat(key string, v float64) {
	s.mu.Lock()
	s.data.Learned[key] = v
	s.dirt = true
	s.mu.Unlock()
}

// Delete forgets a learned value.
func (s *State) Delete(key string) {
	s.mu.Lock()
	delete(s.data.Learned, key)
	s.dirt = true
	s.mu.Unlock()
}

// Learned returns a copy of all learned values.
func (s *State) Learned() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]float64, len(s.data.Learned))
	for k, v := range s.data.Learned {
		out[k] = v
	}
	return out
}

func (s *State) drift() (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.DriftPPM == nil {
		return 0, false
	}
	return *s.data.DriftPPM, true
}

func (s *State) setDrift(ppm float64) {
	s.mu.Lock()
	s.data.DriftPPM = &ppm
	s.dirt = true
	s.mu.Unlock()
}

func (s *State) recordSync(at time.Time, offsetMs float64) {
	s.mu.Lock()
	s.data.LastSync = at
	s.data.LastOffsetMs = offsetMs
	s.dirt = true
	s.mu.Unlock()
}

// LastSync returns the time of the last correction recorded in the state.
func (s *State) LastSync() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastSync
}

// Save writes the state if it changed.
func (s *State) Save() error {
	s.mu.Lock()
	if !s.dirt || s.path == "" {
		s.mu.Unlock()
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	s.dirt = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.path, append(b, '\n'), 0o644)
}
