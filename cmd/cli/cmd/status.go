package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gilang/funnel/internal/daemon"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and torrent counts",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	resp, err := apiClient().Get(apiURL("/api/status"))
	if err != nil {
		fmt.Println("daemon: not running")
		return nil
	}
	defer resp.Body.Close()

	var st daemon.DaemonStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !st.Running {
		fmt.Println("daemon: not running")
		return nil
	}

	fmt.Println("daemon: running")
	order := []daemon.Status{
		daemon.StatusDownloading,
		daemon.StatusSeeding,
		daemon.StatusQueued,
		daemon.StatusPaused,
		daemon.StatusFailed,
	}
	for _, s := range order {
		fmt.Printf("  %-12s %d\n", string(s), st.Counts[s])
	}
	return nil
}
