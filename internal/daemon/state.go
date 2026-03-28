package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var _ StateStore = (*State)(nil)

// SavedTorrent is a torrent entry persisted to disk so the daemon can
// resume it after restart.
type SavedTorrent struct {
	ID     string `json:"id"`
	Magnet string `json:"magnet"`
	Name   string `json:"name,omitempty"`
	Paused bool   `json:"paused,omitempty"`
}

// State manages the persistent list of torrents on disk.
type State struct {
	path     string
	mu       sync.Mutex
	Torrents []SavedTorrent `json:"torrents"`
}

// LoadState reads state from path, or returns an empty State if the file
// does not exist yet.
func LoadState(path string) (*State, error) {
	s := &State{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *State) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Add appends a torrent and persists.
func (s *State) Add(t SavedTorrent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Torrents = append(s.Torrents, t)
	return s.save()
}

// Remove removes a torrent by ID and persists.
func (s *State) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.Torrents[:0]
	for _, t := range s.Torrents {
		if t.ID != id {
			filtered = append(filtered, t)
		}
	}
	s.Torrents = filtered
	return s.save()
}

// Update modifies a saved torrent in place and persists.
func (s *State) Update(id string, fn func(*SavedTorrent)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Torrents {
		if s.Torrents[i].ID == id {
			fn(&s.Torrents[i])
			return s.save()
		}
	}
	return nil // not found is a no-op
}

// List returns a snapshot of saved torrents.
func (s *State) List() []SavedTorrent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SavedTorrent, len(s.Torrents))
	copy(out, s.Torrents)
	return out
}
