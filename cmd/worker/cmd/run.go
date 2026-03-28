package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/anacrolix/torrent/storage"
	"gopkg.gilang.dev/funnel/internal/cluster"
	"gopkg.gilang.dev/funnel/internal/daemon"
	"gopkg.gilang.dev/funnel/internal/store"
	"gopkg.gilang.dev/funnel/storages"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the funnel worker",
	RunE:  runWorker,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runWorker(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	stor, err := buildStorage()
	if err != nil {
		return err
	}

	st := store.NewMemoryStateStore()
	uploadRate := int64(viper.GetInt("upload-rate"))
	maxActive := viper.GetInt("capacity")
	mgr, err := daemon.NewManager(stor, uploadRate, maxActive, st, buildStorageInfo())
	if err != nil {
		return err
	}
	defer mgr.Close()

	managerURL := viper.GetString("manager")
	token := viper.GetString("token")
	if token == "" {
		token = os.Getenv("FUNNEL_JOIN_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("cluster join token is required (flag --token or env FUNNEL_JOIN_TOKEN)")
	}

	agent := cluster.NewAgent(managerURL, token, mgr, maxActive, Version)

	log.Printf("[worker] starting agent (manager=%s, capacity=%d, version=%s)", managerURL, maxActive, Version)
	
	errCh := make(chan error, 1)
	go func() { errCh <- agent.Run(ctx) }()

	select {
	case <-sigCtx.Done():
		log.Println("[worker] shutting down...")
		return nil
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
