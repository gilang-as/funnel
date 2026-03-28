package cmd

import (
	"net/http"

	"github.com/gilang/funnel/internal/ipc"
)

const apiBase = "http://localhost"

func apiClient() *http.Client {
	return ipc.NewHTTPClient()
}

func apiURL(path string) string {
	return apiBase + path
}
