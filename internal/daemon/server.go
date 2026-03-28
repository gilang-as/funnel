package daemon

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/gilang/funnel/internal/ipc"
)

// Server wraps the HTTP daemon.
type Server struct {
	mgr    *Manager
	cancel context.CancelFunc
	srv    *http.Server
}

// NewServer creates a Server that uses IPC transport.
func NewServer(mgr *Manager, cancel context.CancelFunc) *Server {
	s := &Server{mgr: mgr, cancel: cancel}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/torrents", s.handleAdd)
	mux.HandleFunc("GET /api/torrents", s.handleList)
	mux.HandleFunc("PATCH /api/torrents/{id}", s.handleAction)
	mux.HandleFunc("POST /api/torrents/{id}/stop", s.handleStop)
	mux.HandleFunc("DELETE /api/torrents/{id}", s.handleRemove)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)

	s.srv = &http.Server{Handler: mux}
	return s
}

// ListenAndServe starts the HTTP server over IPC transport.
func (s *Server) ListenAndServe() error {
	ln, err := ipc.NewListener()
	if err != nil {
		return err
	}
	log.Printf("[daemon] listening on %s", ipc.SocketPath())
	return s.srv.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Magnet == "" {
		writeErr(w, http.StatusBadRequest, "magnet is required")
		return
	}
	resp, err := s.mgr.Add(req.Magnet)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	code := http.StatusCreated
	if !resp.New {
		code = http.StatusOK
	}
	writeJSON(w, code, resp)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	filter := Status(r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, s.mgr.List(filter))
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var err error
	switch req.Action {
	case "pause":
		err = s.mgr.Pause(id)
	case "resume":
		err = s.mgr.Resume(id)
	default:
		writeErr(w, http.StatusBadRequest, "unknown action: "+req.Action)
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.Stop(id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.Remove(id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.DaemonStatus())
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	go s.cancel()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, ErrorResponse{Error: msg})
}
