package cmd

import (
	"context"
	"log"
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
	"gopkg.gilang.dev/funnel/storages"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the funnel daemon",
	RunE:  runDaemon,
}

func init() {
	rootCmd.AddCommand(daemonCmd)
	daemonCmd.Flags().Int("max-active", 0, "max concurrent downloads (default 3)")
	_ = viper.BindPFlag("max-active", daemonCmd.Flags().Lookup("max-active"))
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
	maxActive := viper.GetInt("max-active")
	mgr, err := daemon.NewManager(stor, uploadRate, maxActive, st, buildStorageInfo())
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
