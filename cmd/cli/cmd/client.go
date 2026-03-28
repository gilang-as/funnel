package cmd

import (
	"net/http"

	"github.com/gilang/funnel/internal/ipc"
)

// apiBase and httpClientOverride can be replaced in tests.
var (
	apiBase            = "http://localhost"
	httpClientOverride *http.Client
)

func apiClient() *http.Client {
	if httpClientOverride != nil {
		return httpClientOverride
	}
	return ipc.NewHTTPClient()
}

func apiURL(path string) string {
	return apiBase + path
}
