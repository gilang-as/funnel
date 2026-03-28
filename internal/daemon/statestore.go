package daemon

// StateStore is the persistence backend for saved torrents.
// Implemented by *State (JSON file), mysqlStateStore, pgStateStore, memoryStateStore.
type StateStore interface {
	List() []SavedTorrent
	Add(t SavedTorrent) error
	Remove(id string) error
	Update(id string, fn func(*SavedTorrent)) error
	// Close releases any underlying resources (e.g. database connections).
	Close() error
}
