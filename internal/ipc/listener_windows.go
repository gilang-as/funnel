//go:build windows

package ipc

import (
	"net"

	"github.com/Microsoft/go-winio"
)

// NewListener creates a new Named Pipe listener.
func NewListener() (net.Listener, error) {
	return winio.ListenPipe(SocketPath(), nil)
}
