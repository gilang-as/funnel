//go:build !windows

package ipc

import (
	"context"
	"net"
	"net/http"
)

// NewHTTPClient returns an *http.Client that dials over a Unix domain socket.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", SocketPath())
			},
		},
	}
}
