package daemon

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"
)

// StorageRemover is implemented by storage backends that support data deletion.
type StorageRemover interface {
	DeleteTorrentData(ctx context.Context, infoHash string) error
}

type managedTorrent struct {
	t      *torrent.Torrent
	magnet string
	name   string
	mu     sync.Mutex
	status Status
}

// Manager owns the anacrolix torrent client and tracks active torrents.
type Manager struct {
	client      *torrent.Client
	torrents    map[string]*managedTorrent
	mu          sync.RWMutex
	state       *State
	stor        storage.ClientImpl
	maxActive   int
	storageInfo StorageInfo
}

// NewManager creates a Manager with the given storage backend, upload rate
// limit (bytes/sec; 0 = unlimited), and max concurrent downloads (0 = default 3).
func NewManager(stor storage.ClientImpl, uploadRate int64, maxActive int, st *State, si StorageInfo) (*Manager, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DefaultStorage = stor
	if uploadRate > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(uploadRate), int(uploadRate))
	}
	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	if maxActive <= 0 {
		maxActive = 3
	}
	m := &Manager{
		client:      client,
		torrents:    make(map[string]*managedTorrent),
		state:       st,
		stor:        stor,
		maxActive:   maxActive,
		storageInfo: si,
	}
	// Re-add torrents from persisted state.
	for _, saved := range st.List() {
		initialStatus := StatusQueued
		if saved.Paused {
			initialStatus = StatusPaused
		}
		if err := m.addMagnet(saved.Magnet, false, initialStatus); err != nil {
			log.Printf("[WARN] resume torrent %s: %v", saved.ID, err)
		}
	}
	return m, nil
}

// Close shuts down the underlying torrent client.
func (m *Manager) Close() {
	m.client.Close()
}

// resolveID resolves a full or prefix ID to the full infoHash.
// Returns an error if not found or ambiguous.
func (m *Manager) resolveID(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.torrents[id]; ok {
		return id, nil
	}
	var matches []string
	for k := range m.torrents {
		if len(k) >= len(id) && k[:len(id)] == id {
			matches = append(matches, k)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("torrent %s not found", id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous id %q matches %d torrents", id, len(matches))
	}
}

// Add adds a magnet link, deduplicating by infoHash.
func (m *Manager) Add(magnet string) (AddResponse, error) {
	t, err := m.client.AddMagnet(magnet)
	if err != nil {
		return AddResponse{}, fmt.Errorf("add magnet: %w", err)
	}
	id := t.InfoHash().HexString()

	m.mu.Lock()
	existing, exists := m.torrents[id]
	if exists {
		existing.mu.Lock()
		st := existing.status
		existing.mu.Unlock()
		m.mu.Unlock()
		return AddResponse{ID: id, Status: st, New: false}, nil
	}
	mt := &managedTorrent{
		t:      t,
		magnet: magnet,
		status: StatusQueued,
	}
	m.torrents[id] = mt
	m.mu.Unlock()

	_ = m.state.Add(SavedTorrent{ID: id, Magnet: magnet})
	go m.watchTorrent(id, mt, StatusQueued)

	return AddResponse{ID: id, Status: StatusQueued, New: true}, nil
}

func (m *Manager) addMagnet(magnet string, persist bool, initialStatus Status) error {
	t, err := m.client.AddMagnet(magnet)
	if err != nil {
		return fmt.Errorf("add magnet: %w", err)
	}
	id := t.InfoHash().HexString()

	mt := &managedTorrent{
		t:      t,
		magnet: magnet,
		status: initialStatus,
	}

	m.mu.Lock()
	m.torrents[id] = mt
	m.mu.Unlock()

	if persist {
		_ = m.state.Add(SavedTorrent{ID: id, Magnet: magnet})
	}

	go m.watchTorrent(id, mt, initialStatus)
	return nil
}

// Pause pauses a torrent (downloading or seeding).
func (m *Manager) Pause(id string) error {
	full, err := m.resolveID(id)
	if err != nil {
		return err
	}
	id = full
	m.mu.RLock()
	mt, ok := m.torrents[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	mt.mu.Lock()
	st := mt.status
	mt.mu.Unlock()

	switch st {
	case StatusDownloading, StatusQueued:
		mt.t.DisallowDataDownload()
		mt.mu.Lock()
		mt.status = StatusPaused
		mt.mu.Unlock()
	case StatusSeeding:
		mt.t.Drop()
		mt.mu.Lock()
		mt.status = StatusPaused
		mt.mu.Unlock()
	default:
		return fmt.Errorf("cannot pause torrent in %s state", st)
	}

	_ = m.state.Update(id, func(s *SavedTorrent) { s.Paused = true })
	return nil
}

// Resume resumes a paused torrent.
func (m *Manager) Resume(id string) error {
	full, err := m.resolveID(id)
	if err != nil {
		return err
	}
	id = full
	m.mu.RLock()
	mt, ok := m.torrents[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	mt.mu.Lock()
	if mt.status != StatusPaused {
		st := mt.status
		mt.mu.Unlock()
		return fmt.Errorf("torrent %s is not paused (status: %s)", id, st)
	}
	mt.mu.Unlock()

	newT, err := m.client.AddMagnet(mt.magnet)
	if err != nil {
		return fmt.Errorf("re-add magnet: %w", err)
	}

	mt.mu.Lock()
	mt.t = newT
	mt.status = StatusQueued
	mt.mu.Unlock()

	_ = m.state.Update(id, func(s *SavedTorrent) { s.Paused = false })
	go m.watchTorrent(id, mt, StatusQueued)
	return nil
}

// Stop disconnects a torrent from the client and removes it from the active
// list. Data is retained. Works from any state.
func (m *Manager) Stop(id string) error {
	full, err := m.resolveID(id)
	if err != nil {
		return err
	}
	id = full
	m.mu.Lock()
	mt, ok := m.torrents[id]
	if ok {
		delete(m.torrents, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	mt.t.Drop()
	m.processQueue()
	return m.state.Remove(id)
}

// Remove removes a torrent from the active list and deletes its data.
func (m *Manager) Remove(id string) error {
	full, err := m.resolveID(id)
	if err != nil {
		return err
	}
	id = full
	m.mu.Lock()
	mt, ok := m.torrents[id]
	if ok {
		delete(m.torrents, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	mt.t.Drop()

	if remover, ok := m.stor.(StorageRemover); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := remover.DeleteTorrentData(ctx, id); err != nil {
			log.Printf("[WARN] delete data for %s: %v", id, err)
		}
	}

	m.processQueue()
	return m.state.Remove(id)
}

// List returns info about torrents, optionally filtered by status.
func (m *Manager) List(filter Status) []TorrentInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]TorrentInfo, 0, len(m.torrents))
	for id, mt := range m.torrents {
		mt.mu.Lock()
		st := mt.status
		name := mt.name
		mag := mt.magnet
		t := mt.t
		mt.mu.Unlock()

		if filter != "" && st != filter {
			continue
		}

		info := TorrentInfo{
			ID:     id,
			Name:   name,
			Magnet: mag,
			Status: st,
		}
		if t != nil {
			info.Size = t.Length()
			info.Peers = t.Stats().ActivePeers
			if t.Length() > 0 {
				info.Progress = float64(t.BytesCompleted()) / float64(t.Length()) * 100
			}
		}
		out = append(out, info)
	}
	return out
}

// DaemonStatus returns aggregate status counts.
func (m *Manager) DaemonStatus() DaemonStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := map[Status]int{
		StatusQueued:      0,
		StatusDownloading: 0,
		StatusSeeding:     0,
		StatusPaused:      0,
		StatusFailed:      0,
	}
	for _, mt := range m.torrents {
		mt.mu.Lock()
		counts[mt.status]++
		mt.mu.Unlock()
	}
	return DaemonStatus{Running: true, Counts: counts, Storage: m.storageInfo}
}

// countActive returns the number of torrents actively downloading.
// Caller must not hold m.mu.
func (m *Manager) countActive() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, mt := range m.torrents {
		mt.mu.Lock()
		if mt.status == StatusDownloading {
			count++
		}
		mt.mu.Unlock()
	}
	return count
}

// tryStart promotes a queued torrent to downloading if a slot is available.
func (m *Manager) tryStart(id string) {
	m.mu.RLock()
	mt, ok := m.torrents[id]
	m.mu.RUnlock()
	if !ok {
		return
	}

	mt.mu.Lock()
	if mt.status != StatusQueued {
		mt.mu.Unlock()
		return
	}
	mt.mu.Unlock()

	if m.countActive() < m.maxActive {
		mt.mu.Lock()
		if mt.status == StatusQueued {
			mt.t.DownloadAll()
			mt.status = StatusDownloading
			log.Printf("[torrent] downloading: %s", id)
		}
		mt.mu.Unlock()
	}
}

// processQueue starts queued torrents up to maxActive.
func (m *Manager) processQueue() {
	m.mu.RLock()
	var queued []string
	for id, mt := range m.torrents {
		mt.mu.Lock()
		if mt.status == StatusQueued {
			queued = append(queued, id)
		}
		mt.mu.Unlock()
	}
	m.mu.RUnlock()

	for _, id := range queued {
		if m.countActive() >= m.maxActive {
			break
		}
		m.tryStart(id)
	}
}

// watchTorrent runs as a goroutine: waits for GotInfo then monitors
// status transitions for the given managed torrent.
func (m *Manager) watchTorrent(id string, mt *managedTorrent, initialStatus Status) {
	// Snapshot the torrent at start; it may be replaced by Resume.
	mt.mu.Lock()
	t := mt.t
	mt.mu.Unlock()

	<-t.GotInfo()

	// Update cached name.
	mt.mu.Lock()
	mt.name = t.Name()
	mt.mu.Unlock()
	_ = m.state.Update(id, func(s *SavedTorrent) { s.Name = t.Name() })

	if initialStatus != StatusPaused {
		m.tryStart(id)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		// Exit if removed from map.
		m.mu.RLock()
		_, exists := m.torrents[id]
		m.mu.RUnlock()
		if !exists {
			return
		}

		mt.mu.Lock()
		st := mt.status
		currentT := mt.t
		mt.mu.Unlock()

		// Exit if paused or failed — a new goroutine will be launched on resume.
		if st == StatusPaused || st == StatusFailed {
			return
		}

		if st == StatusDownloading && isComplete(currentT) {
			mt.mu.Lock()
			// Guard: only transition if torrent hasn't been swapped out.
			if mt.status == StatusDownloading && mt.t == currentT {
				mt.status = StatusSeeding
				log.Printf("[torrent] seeding: %s (%s)", mt.name, id)
			}
			mt.mu.Unlock()
			m.processQueue()
		}
	}
}

func isComplete(t *torrent.Torrent) bool {
	n := t.NumPieces()
	return n > 0 && t.Stats().PiecesComplete >= n
}
