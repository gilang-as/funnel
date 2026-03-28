package store

import (
	"sync"

	"gopkg.gilang.dev/funnel/internal/daemon"
)

type memoryStateStore struct {
	mu       sync.Mutex
	torrents map[string]daemon.SavedTorrent
}

// NewMemoryStateStore creates a transient, in-memory StateStore.
func NewMemoryStateStore() daemon.StateStore {
	return &memoryStateStore{
		torrents: make(map[string]daemon.SavedTorrent),
	}
}

func (s *memoryStateStore) List() []daemon.SavedTorrent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]daemon.SavedTorrent, 0, len(s.torrents))
	for _, t := range s.torrents {
		out = append(out, t)
	}
	return out
}

func (s *memoryStateStore) Add(t daemon.SavedTorrent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.torrents[t.ID] = t
	return nil
}

func (s *memoryStateStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.torrents, id)
	return nil
}

func (s *memoryStateStore) Update(id string, fn func(*daemon.SavedTorrent)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.torrents[id]
	if !ok {
		return nil
	}
	fn(&t)
	s.torrents[id] = t
	return nil
}
