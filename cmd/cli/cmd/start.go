package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the funnel daemon in the background",
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	if isDaemonRunning() {
		fmt.Println("daemon already running")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	if err := spawnDaemon(exe); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}

	// Wait until socket is reachable.
	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if isDaemonRunning() {
			fmt.Println("daemon started")
			return nil
		}
	}
	return fmt.Errorf("daemon failed to start within 2s")
}

func isDaemonRunning() bool {
	client := apiClient()
	resp, err := client.Get(apiURL("/api/status"))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
