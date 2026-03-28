package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/anacrolix/torrent/storage"
	"github.com/gilang/funnel/internal/daemon"
	"github.com/gilang/funnel/storages"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the funnel daemon",
	RunE:  runDaemon,
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	stor, err := buildStorage()
	if err != nil {
		return err
	}

	statePath := defaultStatePath()
	st, err := daemon.LoadState(statePath)
	if err != nil {
		return err
	}

	uploadRate := int64(viper.GetInt("upload-rate"))
	mgr, err := daemon.NewManager(stor, uploadRate, st)
	if err != nil {
		return err
	}
	defer mgr.Close()

	srv := daemon.NewServer(mgr, cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-sigCtx.Done():
		log.Println("[daemon] shutting down...")
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
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
