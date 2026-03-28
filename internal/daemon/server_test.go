package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockManager implements managerIface for testing.
type mockManager struct {
	addFunc    func(magnet string) (AddResponse, error)
	listFunc   func(filter Status) []TorrentInfo
	pauseFunc  func(id string) error
	resumeFunc func(id string) error
	stopFunc   func(id string) error
	removeFunc func(id string) error
	statusFunc func() DaemonStatus
}

func (m *mockManager) Add(magnet string) (AddResponse, error) {
	return m.addFunc(magnet)
}
func (m *mockManager) List(filter Status) []TorrentInfo {
	return m.listFunc(filter)
}
func (m *mockManager) Pause(id string) error  { return m.pauseFunc(id) }
func (m *mockManager) Resume(id string) error { return m.resumeFunc(id) }
func (m *mockManager) Stop(id string) error   { return m.stopFunc(id) }
func (m *mockManager) Remove(id string) error { return m.removeFunc(id) }
func (m *mockManager) DaemonStatus() DaemonStatus {
	return m.statusFunc()
}

func newTestServer(mgr managerIface) *httptest.Server {
	srv := &Server{mgr: mgr, cancel: func() {}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/torrents", srv.handleAdd)
	mux.HandleFunc("GET /api/torrents", srv.handleList)
	mux.HandleFunc("PATCH /api/torrents/{id}", srv.handleAction)
	mux.HandleFunc("POST /api/torrents/{id}/stop", srv.handleStop)
	mux.HandleFunc("DELETE /api/torrents/{id}", srv.handleRemove)
	mux.HandleFunc("GET /api/status", srv.handleStatus)
	mux.HandleFunc("POST /api/shutdown", srv.handleShutdown)
	srv.srv = &http.Server{Handler: mux}
	return httptest.NewServer(mux)
}

func mustDo(t *testing.T, c *http.Client, req *http.Request) *http.Response {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", req.Method, req.URL, err)
	}
	return resp
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustPost(t *testing.T, url, ct, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, ct, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestHandleAdd_New(t *testing.T) {
	mgr := &mockManager{
		addFunc: func(magnet string) (AddResponse, error) {
			return AddResponse{ID: "abc123", Status: StatusQueued, New: true}, nil
		},
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	resp := mustPost(t, ts.URL+"/api/torrents", "application/json", `{"magnet":"magnet:?xt=abc"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var out AddResponse
	json.NewDecoder(resp.Body).Decode(&out)
	if out.ID != "abc123" || !out.New || out.Status != StatusQueued {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestHandleAdd_Duplicate(t *testing.T) {
	mgr := &mockManager{
		addFunc: func(magnet string) (AddResponse, error) {
			return AddResponse{ID: "abc123", Status: StatusSeeding, New: false}, nil
		},
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	resp := mustPost(t, ts.URL+"/api/torrents", "application/json", `{"magnet":"magnet:?xt=abc"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestHandleAdd_BadJSON(t *testing.T) {
	ts := newTestServer(&mockManager{})
	defer ts.Close()
	resp := mustPost(t, ts.URL+"/api/torrents", "application/json", "{bad")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandleAdd_MissingMagnet(t *testing.T) {
	ts := newTestServer(&mockManager{})
	defer ts.Close()
	resp := mustPost(t, ts.URL+"/api/torrents", "application/json", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandleList(t *testing.T) {
	mgr := &mockManager{
		listFunc: func(filter Status) []TorrentInfo {
			if filter != StatusSeeding {
				t.Errorf("unexpected filter: %s", filter)
			}
			return []TorrentInfo{{ID: "x", Status: StatusSeeding}}
		},
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	resp := mustGet(t, ts.URL+"/api/torrents?status=seeding")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out []TorrentInfo
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out) != 1 || out[0].ID != "x" {
		t.Fatalf("unexpected list: %+v", out)
	}
}

func TestHandleAction_Pause(t *testing.T) {
	var paused string
	mgr := &mockManager{
		pauseFunc: func(id string) error { paused = id; return nil },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/torrents/abc",
		bytes.NewBufferString(`{"action":"pause"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if paused != "abc" {
		t.Fatalf("Pause not called with correct id: %q", paused)
	}
}

func TestHandleAction_Resume(t *testing.T) {
	var resumed string
	mgr := &mockManager{
		resumeFunc: func(id string) error { resumed = id; return nil },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/torrents/abc",
		bytes.NewBufferString(`{"action":"resume"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if resumed != "abc" {
		t.Fatalf("Resume not called with correct id")
	}
}

func TestHandleAction_Unknown(t *testing.T) {
	ts := newTestServer(&mockManager{})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/torrents/abc",
		bytes.NewBufferString(`{"action":"explode"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandleAction_Error(t *testing.T) {
	mgr := &mockManager{
		pauseFunc: func(id string) error { return fmt.Errorf("cannot pause") },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/torrents/abc",
		bytes.NewBufferString(`{"action":"pause"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandleStop(t *testing.T) {
	var stopped string
	mgr := &mockManager{
		stopFunc: func(id string) error { stopped = id; return nil },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/torrents/xyz/stop", nil)
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if stopped != "xyz" {
		t.Fatalf("Stop not called with correct id")
	}
}

func TestHandleRemove(t *testing.T) {
	var removed string
	mgr := &mockManager{
		removeFunc: func(id string) error { removed = id; return nil },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/torrents/xyz", nil)
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if removed != "xyz" {
		t.Fatalf("Remove not called with correct id")
	}
}

func TestHandleRemove_NotFound(t *testing.T) {
	mgr := &mockManager{
		removeFunc: func(id string) error { return fmt.Errorf("not found") },
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/torrents/nope", nil)
	resp := mustDo(t, ts.Client(), req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestHandleStatus(t *testing.T) {
	mgr := &mockManager{
		statusFunc: func() DaemonStatus {
			return DaemonStatus{Running: true, Counts: map[Status]int{StatusDownloading: 2}}
		},
	}
	ts := newTestServer(mgr)
	defer ts.Close()

	resp := mustGet(t, ts.URL+"/api/status")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out DaemonStatus
	json.NewDecoder(resp.Body).Decode(&out)
	if !out.Running || out.Counts[StatusDownloading] != 2 {
		t.Fatalf("unexpected status: %+v", out)
	}
}

func TestHandleShutdown(t *testing.T) {
	cancelled := make(chan struct{})
	srv := &Server{
		mgr:    &mockManager{},
		cancel: func() { close(cancelled) },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shutdown", srv.handleShutdown)
	srv.srv = &http.Server{Handler: mux}
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := mustPost(t, ts.URL+"/api/shutdown", "", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	select {
	case <-cancelled:
	case <-context.Background().Done():
		t.Fatal("cancel not called")
	}
}
