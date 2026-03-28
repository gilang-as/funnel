//go:build windows

package ipc

import (
	"context"
	"net/http"

	"github.com/Microsoft/go-winio"
)

// NewHTTPClient returns an *http.Client that dials over a Named Pipe.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return winio.DialPipeContext(ctx, SocketPath())
			},
		},
	}
}
