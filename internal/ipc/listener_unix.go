//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
)

// NewListener creates a new Unix domain socket listener.
func NewListener() (net.Listener, error) {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	os.Remove(path) // remove stale socket
	return net.Listen("unix", path)
}
