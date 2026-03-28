package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"gopkg.gilang.dev/funnel/internal/daemon"
)

type Agent struct {
	managerURL string
	token      string
	workerID   string
	capacity   int
	version    string
	mgr        *daemon.Manager
	client     *http.Client
}

func NewAgent(managerURL, token string, mgr *daemon.Manager, capacity int, version string) *Agent {
	return &Agent{
		managerURL: managerURL,
		token:      token,
		capacity:   capacity,
		version:    version,
		mgr:        mgr,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *Agent) Run(ctx context.Context) error {
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("initial register: %w", err)
	}

	tickerClaim    := time.NewTicker(5 * time.Second)
	tickerHB       := time.NewTicker(15 * time.Second)
	tickerProgress := time.NewTicker(10 * time.Second)
	defer tickerClaim.Stop()
	defer tickerHB.Stop()
	defer tickerProgress.Stop()

	for {
		select {
		case <-ctx.Done():
			a.drain(context.Background())
			_ = a.leave(context.Background())
			return nil
		case <-tickerClaim.C:
			if err := a.claimJob(ctx); err != nil {
				log.Printf("[agent] claim error: %v", err)
			}
		case <-tickerHB.C:
			if err := a.heartbeat(ctx); err != nil {
				log.Printf("[agent] heartbeat error: %v", err)
			}
		case <-tickerProgress.C:
			if err := a.reportProgress(ctx); err != nil {
				log.Printf("[agent] progress error: %v", err)
			}
		}
	}
}

func (a *Agent) register(ctx context.Context) error {
	req := RegisterReq{
		WorkerID: a.workerID,
		Capacity: a.capacity,
		Version:  a.version,
	}
	var res RegisterRes
	if err := a.request(ctx, "POST", "/internal/workers/register", req, &res); err != nil {
		return err
	}
	a.workerID = res.WorkerID
	log.Printf("[agent] registered as worker %s (capacity=%d, version=%s)", a.workerID, a.capacity, a.version)
	return nil
}

// claimJob asks the manager for the next available job.
// The claim is atomic on the manager side — no two workers can receive the same job.
func (a *Agent) claimJob(ctx context.Context) error {
	active := 0
	for _, t := range a.mgr.List("") {
		if t.Status == daemon.StatusDownloading || t.Status == daemon.StatusQueued {
			active++
		}
	}
	if active >= a.capacity {
		return nil
	}

	req := ClaimReq{WorkerID: a.workerID}
	var res ClaimRes
	if err := a.request(ctx, "POST", "/internal/jobs/claim", req, &res); err != nil {
		return err
	}
	if res.Job == nil {
		return nil
	}

	log.Printf("[agent] claimed job %s", res.Job.JobID)
	if _, err := a.mgr.Add(res.Job.Magnet); err != nil {
		log.Printf("[agent] error starting job %s: %v", res.Job.JobID, err)
		return a.reportFailure(ctx, res.Job.JobID, err.Error())
	}
	return nil
}

func (a *Agent) heartbeat(ctx context.Context) error {
	active := 0
	for _, t := range a.mgr.List("") {
		if t.Status == daemon.StatusDownloading || t.Status == daemon.StatusQueued {
			active++
		}
	}
	req := HeartbeatReq{ActiveJobs: active}
	return a.request(ctx, "POST", "/internal/workers/"+a.workerID+"/heartbeat", req, nil)
}

func (a *Agent) reportProgress(ctx context.Context) error {
	for _, t := range a.mgr.List("") {
		req := ProgressReq{
			Progress: t.Progress,
			Status:   string(t.Status),
			Name:     t.Name,
			Size:     t.Size,
			Peers:    t.Peers,
		}
		if err := a.request(ctx, "POST", "/internal/jobs/"+t.ID+"/progress", req, nil); err != nil {
			log.Printf("[agent] progress report error for %s: %v", t.ID, err)
		}
	}
	return nil
}

// drain is called on graceful shutdown:
//   - downloading jobs → requeue (another worker will pick them up)
//   - seeding jobs      → mark done (user re-adds manually if needed)
func (a *Agent) drain(ctx context.Context) {
	for _, t := range a.mgr.List("") {
		switch t.Status {
		case daemon.StatusDownloading, daemon.StatusQueued:
			if err := a.request(ctx, "POST", "/internal/jobs/"+t.ID+"/requeue", nil, nil); err != nil {
				log.Printf("[agent] requeue error for %s: %v", t.ID, err)
			}
		case daemon.StatusSeeding:
			if err := a.request(ctx, "POST", "/internal/jobs/"+t.ID+"/complete", nil, nil); err != nil {
				log.Printf("[agent] complete error for %s: %v", t.ID, err)
			}
		}
	}
}

func (a *Agent) reportFailure(ctx context.Context, jobID, errMsg string) error {
	req := struct {
		Error string `json:"error"`
	}{Error: errMsg}
	return a.request(ctx, "POST", "/internal/jobs/"+jobID+"/fail", req, nil)
}

func (a *Agent) leave(ctx context.Context) error {
	return a.request(ctx, "DELETE", "/internal/workers/"+a.workerID, nil, nil)
}

func (a *Agent) request(ctx context.Context, method, path string, body, res any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, a.managerURL+path, &buf)
	if err != nil {
		return err
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}
	if res != nil {
		return json.NewDecoder(resp.Body).Decode(res)
	}
	return nil
}
