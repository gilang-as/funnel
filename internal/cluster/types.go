package cluster

// RegisterReq is sent by worker on startup.
type RegisterReq struct {
	WorkerID string `json:"worker_id,omitempty"` // empty = new registration
	Address  string `json:"address"`
	Capacity int    `json:"capacity"`
	Version  string `json:"version"`
}

// RegisterRes is returned by POST /internal/workers/register.
type RegisterRes struct {
	WorkerID string `json:"worker_id"`
}

// ClaimReq is sent by worker to atomically claim the next queued job.
type ClaimReq struct {
	WorkerID string `json:"worker_id"`
}

// ClaimRes is returned by POST /internal/jobs/claim.
// Job is nil when no job is available.
type ClaimRes struct {
	Job *JobAssignment `json:"job"`
}

// JobAssignment carries the info a worker needs to start a job.
type JobAssignment struct {
	JobID    string `json:"job_id"`
	Magnet   string `json:"magnet"`
	InfoHash string `json:"info_hash"`
}

// ProgressReq is body for POST /internal/jobs/{id}/progress.
type ProgressReq struct {
	Progress float64 `json:"progress"`
	Status   string  `json:"status"` // "downloading" | "seeding"
	Name     string  `json:"name,omitempty"`
	Size     int64   `json:"size,omitempty"`
	Peers    int     `json:"peers,omitempty"`
}

// HeartbeatReq is body for POST /internal/workers/{id}/heartbeat.
type HeartbeatReq struct {
	ActiveJobs int `json:"active_jobs"`
}
