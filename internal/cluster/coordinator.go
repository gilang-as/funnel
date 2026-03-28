package cluster

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gopkg.gilang.dev/funnel/internal/store"
)

const staleThreshold = time.Minute

type Coordinator struct {
	store store.Store
}

func NewCoordinator(s store.Store) *Coordinator {
	return &Coordinator{store: s}
}

func (c *Coordinator) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/workers/register", c.handleRegister)
	mux.HandleFunc("POST /internal/workers/{id}/heartbeat", c.handleHeartbeat)
	mux.HandleFunc("DELETE /internal/workers/{id}", c.handleLeave)
	// Atomic job claim — replaces the old racy GET /internal/jobs/next
	mux.HandleFunc("POST /internal/jobs/claim", c.handleClaimJob)
	mux.HandleFunc("POST /internal/jobs/{id}/progress", c.handleProgress)
	mux.HandleFunc("POST /internal/jobs/{id}/complete", c.handleComplete)
	mux.HandleFunc("POST /internal/jobs/{id}/requeue", c.handleRequeue)
	mux.HandleFunc("POST /internal/jobs/{id}/fail", c.handleFail)
}

func (c *Coordinator) Start(ctx context.Context) {
	go c.staleWorkerLoop(ctx)
}

// ── Worker registration ───────────────────────────────────────────────────────

func (c *Coordinator) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id := req.WorkerID
	if id == "" {
		id = uuid.New().String()
	}

	info := &store.WorkerInfo{
		ID:       id,
		Address:  req.Address,
		Capacity: req.Capacity,
		Status:   "active",
		Version:  req.Version,
	}
	if err := c.store.Workers().Upsert(r.Context(), info); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[coordinator] worker registered: %s (capacity=%d, version=%s)", id, req.Capacity, req.Version)
	if err := json.NewEncoder(w).Encode(RegisterRes{WorkerID: id}); err != nil {
		log.Printf("[coordinator] write register response: %v", err)
	}
}

func (c *Coordinator) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req HeartbeatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	wrk, err := c.store.Workers().Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wrk == nil {
		http.Error(w, "worker not found", http.StatusNotFound)
		return
	}

	wrk.ActiveJobs = req.ActiveJobs
	wrk.Status = "active"
	if err := c.store.Workers().Upsert(r.Context(), wrk); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLeave is called on graceful worker shutdown.
// The worker drains its own jobs before calling this, so ReleaseFromWorker
// here is a safety net for any jobs not yet reported.
func (c *Coordinator) handleLeave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := c.store.Jobs().ReleaseFromWorker(r.Context(), id); err != nil {
		log.Printf("[coordinator] error releasing jobs for worker %s: %v", id, err)
	}
	if err := c.store.Workers().Remove(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[coordinator] worker left: %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// ── Job claiming ──────────────────────────────────────────────────────────────

// handleClaimJob atomically assigns the oldest queued job to the requesting worker.
// Uses FOR UPDATE SKIP LOCKED so concurrent workers cannot claim the same job.
func (c *Coordinator) handleClaimJob(w http.ResponseWriter, r *http.Request) {
	var req ClaimReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.WorkerID == "" {
		http.Error(w, "worker_id required", http.StatusBadRequest)
		return
	}

	job, err := c.store.Jobs().Claim(r.Context(), req.WorkerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// No job available — return empty response, worker will retry on next tick.
	if job == nil {
		if err := json.NewEncoder(w).Encode(ClaimRes{Job: nil}); err != nil {
			log.Printf("[coordinator] write claim response: %v", err)
		}
		return
	}

	log.Printf("[coordinator] job %s claimed by worker %s", job.ID, req.WorkerID)
	if err := json.NewEncoder(w).Encode(ClaimRes{Job: &JobAssignment{
		JobID:    job.ID,
		Magnet:   job.Magnet,
		InfoHash: job.InfoHash,
	}}); err != nil {
		log.Printf("[coordinator] write claim response: %v", err)
	}
}

// ── Job progress & lifecycle ──────────────────────────────────────────────────

func (c *Coordinator) handleProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ProgressReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := c.store.Jobs().Update(r.Context(), id, func(j *store.Job) {
		j.Progress = req.Progress
		j.Status = store.JobStatus(req.Status)
		if req.Name != "" {
			j.Name = req.Name
		}
		if req.Size > 0 {
			j.Size = req.Size
		}
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Coordinator) handleComplete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := c.store.Jobs().Update(r.Context(), id, func(j *store.Job) {
		j.Status = store.JobDone
		j.Progress = 100
		now := time.Now()
		j.CompletedAt = &now
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRequeue resets a job back to queued so another worker can pick it up.
// Called by a worker before graceful shutdown for its downloading jobs.
func (c *Coordinator) handleRequeue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := c.store.Jobs().Update(r.Context(), id, func(j *store.Job) {
		j.Status = store.JobQueued
		j.WorkerID = ""
		j.Progress = 0
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Coordinator) handleFail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := c.store.Jobs().Update(r.Context(), id, func(j *store.Job) {
		j.Status = store.JobFailed
		j.ErrorMsg = req.Error
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Stale worker GC ───────────────────────────────────────────────────────────

// staleWorkerLoop runs every 30 s. For each worker that has missed its heartbeat:
//  1. Release their jobs (downloading → queued, seeding → done)
//  2. Mark the worker offline
func (c *Coordinator) staleWorkerLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.handleStaleWorkers(ctx)
		}
	}
}

func (c *Coordinator) handleStaleWorkers(ctx context.Context) {
	ids, err := c.store.Workers().StaleIDs(ctx, staleThreshold)
	if err != nil {
		log.Printf("[coordinator] error fetching stale worker IDs: %v", err)
		return
	}

	for _, id := range ids {
		if err := c.store.Jobs().ReleaseFromWorker(ctx, id); err != nil {
			log.Printf("[coordinator] error releasing jobs for stale worker %s: %v", id, err)
			continue
		}
		log.Printf("[coordinator] released jobs for stale worker %s", id)
	}

	if len(ids) > 0 {
		if err := c.store.Workers().MarkStale(ctx, staleThreshold); err != nil {
			log.Printf("[coordinator] error marking stale workers: %v", err)
		}
	}
}
