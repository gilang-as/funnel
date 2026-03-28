package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/anacrolix/torrent/storage"
	"gopkg.gilang.dev/funnel/internal/daemon"
	"gopkg.gilang.dev/funnel/internal/store"
	"gopkg.gilang.dev/funnel/storages"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the standalone funnel daemon on TCP",
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

	stor, err := buildStorage()
	if err != nil {
		return err
	}

	st, err := buildStateStore()
	if err != nil {
		return err
	}

	uploadRate := int64(viper.GetInt("upload-rate"))
	maxActive := viper.GetInt("max-active")
	mgr, err := daemon.NewManager(stor, uploadRate, maxActive, st, buildStorageInfo())
	if err != nil {
		return err
	}
	defer mgr.Close()

	srv := daemon.NewServer(mgr, cancel)

	port := viper.GetInt("port")
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-sigCtx.Done():
		log.Println("[standalone] shutting down...")
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

func buildStateStore() (daemon.StateStore, error) {
	stateType := viper.GetString("state")
	dsn := viper.GetString("db-dsn")

	switch stateType {
	case "mysql":
		if dsn == "" {
			return nil, fmt.Errorf("--db-dsn is required for mysql state")
		}
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, err
		}
		return store.NewMySQLStateStore(db)
	case "postgres":
		if dsn == "" {
			return nil, fmt.Errorf("--db-dsn is required for postgres state")
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, err
		}
		return store.NewPostgresStateStore(db)
	default:
		return daemon.LoadState(defaultStatePath())
	}
}

func buildStorageInfo() daemon.StorageInfo {
	storType := viper.GetString("storage.type")
	if storType == "s3" {
		endpoint := strings.TrimRight(viper.GetString("storage.s3.endpoint"), "/")
		bucket := viper.GetString("storage.s3.bucket")
		baseDir := strings.Trim(viper.GetString("storage.s3.base-dir"), "/")
		loc := endpoint + "/" + bucket
		if baseDir != "" && baseDir != "." {
			loc += "/" + baseDir
		}
		return daemon.StorageInfo{Type: "s3", Location: loc}
	}
	dir := viper.GetString("storage.local.dir")
	if len(dir) > 1 && dir[:2] == "~/" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	return daemon.StorageInfo{Type: "local", Location: dir}
}

func buildStorage() (storage.ClientImpl, error) {
	storType := viper.GetString("storage.type")
	switch storType {
	case "s3":
		cfg := storages.S3Config{
			Bucket:    viper.GetString("storage.s3.bucket"),
			Endpoint:  viper.GetString("storage.s3.endpoint"),
			AccessKey: viper.GetString("storage.s3.access-key"),
			SecretKey: viper.GetString("storage.s3.secret-key"),
			Region:    viper.GetString("storage.s3.region"),
			BaseDir:   viper.GetString("storage.s3.base-dir"),
		}
		return storages.NewS3Storage(cfg)
	default:
		dir := viper.GetString("storage.local.dir")
		if len(dir) > 1 && dir[:2] == "~/" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, dir[2:])
		}
		return storages.NewLocalStorage(dir), nil
	}
}

func defaultStatePath() string {
	switch runtime.GOOS {
	case "windows":
		base, _ := os.UserConfigDir()
		return filepath.Join(base, "funnel", "state.json")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "funnel", "state.json")
	default:
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(base, "funnel", "state.json")
	}
}
