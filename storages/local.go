package storages

import (
	"context"
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent/storage"
)

type localStorageImpl struct {
	storage.ClientImpl
	dir string
}

// DeleteTorrentData removes the torrent's data directory from local disk.
func (l *localStorageImpl) DeleteTorrentData(_ context.Context, infoHash string) error {
	return os.RemoveAll(filepath.Join(l.dir, infoHash))
}

// NewLocalStorage returns an anacrolix/torrent storage backend that stores
// files in the given directory on local disk.
func NewLocalStorage(dir string) storage.ClientImpl {
	return &localStorageImpl{
		ClientImpl: storage.NewFile(dir),
		dir:        dir,
	}
}
