package daemon

// Status represents the current state of a torrent.
type Status string

const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusSeeding     Status = "seeding"
	StatusPaused      Status = "paused"
	StatusFailed      Status = "failed"
)

// DaemonStatus holds aggregate counts per status.
type DaemonStatus struct {
	Running bool           `json:"running"`
	Counts  map[Status]int `json:"counts"`
}

// TorrentInfo is the public representation of a managed torrent.
type TorrentInfo struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Magnet   string  `json:"magnet"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Status   Status  `json:"status"`
	Peers    int     `json:"peers"`
}

// AddRequest is the body for POST /api/torrents.
type AddRequest struct {
	Magnet string `json:"magnet"`
}

// AddResponse is returned after successfully adding a torrent.
type AddResponse struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	New    bool   `json:"new"`
}

// ActionRequest is the body for PATCH /api/torrents/{id}.
type ActionRequest struct {
	Action string `json:"action"`
}

// ErrorResponse wraps an error message for JSON responses.
type ErrorResponse struct {
	Error string `json:"error"`
}
