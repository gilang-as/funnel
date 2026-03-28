package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"gopkg.gilang.dev/funnel/internal/cluster"
	"gopkg.gilang.dev/funnel/internal/daemon"
	"gopkg.gilang.dev/funnel/internal/store"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the funnel cluster manager",
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	driver := viper.GetString("db-driver")
	dsn := viper.GetString("db-dsn")
	if driver == "" || dsn == "" {
		return fmt.Errorf("--db-driver and --db-dsn are required")
	}

	var s store.Store
	var err error
	switch driver {
	case "mysql":
		s, err = store.NewMySQLStore(dsn)
	case "postgres":
		s, err = store.NewPostgresStore(dsn)
	default:
		return fmt.Errorf("unsupported db-driver: %s", driver)
	}
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer s.Close()

	if err := s.RunMigrations(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	coord := cluster.NewCoordinator(s)
	coord.Start(ctx)

	// dbManager proxies daemon API to SQL store
	dbMgr := &dbManager{store: s}
	srv := daemon.NewServerCustom(dbMgr, cancel)

	// Combine routes
	mux := http.NewServeMux()
	// Public API
	srv.RegisterRoutes(mux)
	// Internal Cluster API with Auth
	internalMux := http.NewServeMux()
	coord.RegisterRoutes(internalMux)
	mux.Handle("/internal/", authMiddleware(s, internalMux))

	port := viper.GetInt("port")
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}

	httpSrv := &http.Server{Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[manager] listening on %s", ln.Addr())
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-sigCtx.Done():
		log.Println("[manager] shutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		return httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func authMiddleware(s store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		hash := cluster.HashToken(token)

		t, err := s.Tokens().GetByHash(r.Context(), hash)
		if err != nil || t == nil || t.Revoked {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type dbManager struct {
	store store.Store
}

// dbCtx returns a context with a 10-second timeout for database operations.
func dbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func (m *dbManager) Add(magnet string) (daemon.AddResponse, error) {
	ctx, cancel := dbCtx()
	defer cancel()
	id := uuid.New().String()
	job := &store.Job{
		ID:     id,
		Magnet: magnet,
		Status: store.JobQueued,
	}
	if err := m.store.Jobs().Create(ctx, job); err != nil {
		return daemon.AddResponse{}, err
	}
	return daemon.AddResponse{ID: id, Status: daemon.StatusQueued, New: true}, nil
}

func (m *dbManager) List(filter daemon.Status) []daemon.TorrentInfo {
	ctx, cancel := dbCtx()
	defer cancel()
	jobs, err := m.store.Jobs().List(ctx, store.JobFilter{
		Status: store.JobStatus(filter),
	})
	if err != nil {
		return nil
	}
	out := make([]daemon.TorrentInfo, len(jobs))
	for i, j := range jobs {
		out[i] = daemon.TorrentInfo{
			ID:       j.ID,
			Name:     j.Name,
			Magnet:   j.Magnet,
			Status:   daemon.Status(j.Status),
			Size:     j.Size,
			Progress: j.Progress,
		}
	}
	return out
}

func (m *dbManager) Pause(id string) error {
	ctx, cancel := dbCtx()
	defer cancel()
	return m.store.Jobs().Update(ctx, id, func(j *store.Job) {
		j.Paused = true
		j.Status = store.JobPaused
	})
}

func (m *dbManager) Resume(id string) error {
	ctx, cancel := dbCtx()
	defer cancel()
	return m.store.Jobs().Update(ctx, id, func(j *store.Job) {
		j.Paused = false
		j.Status = store.JobQueued
	})
}

func (m *dbManager) Stop(id string) error {
	ctx, cancel := dbCtx()
	defer cancel()
	return m.store.Jobs().Update(ctx, id, func(j *store.Job) {
		j.Status = store.JobPaused
	})
}

func (m *dbManager) Remove(id string) error {
	ctx, cancel := dbCtx()
	defer cancel()
	return m.store.Jobs().Delete(ctx, id)
}

func (m *dbManager) DaemonStatus() daemon.DaemonStatus {
	ctx, cancel := dbCtx()
	defer cancel()
	jobs, _ := m.store.Jobs().List(ctx, store.JobFilter{})
	counts := make(map[daemon.Status]int)
	for _, j := range jobs {
		counts[daemon.Status(j.Status)]++
	}
	ctx2, cancel2 := dbCtx()
	defer cancel2()
	workers, _ := m.store.Workers().List(ctx2)
	activeWorkers := 0
	for _, w := range workers {
		if w.Status == "active" {
			activeWorkers++
		}
	}
	return daemon.DaemonStatus{
		Running: true,
		Counts:  counts,
		Storage: daemon.StorageInfo{Type: "cluster", Location: fmt.Sprintf("%d workers", activeWorkers)},
	}
}
