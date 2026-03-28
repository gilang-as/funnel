package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var shutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Gracefully shut down the running daemon",
	RunE:  runShutdown,
}

func init() {
	rootCmd.AddCommand(shutdownCmd)
}

func runShutdown(cmd *cobra.Command, args []string) error {
	resp, err := apiClient().Post(apiURL("/api/shutdown"), "application/json", nil)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected response: %s", resp.Status)
	}
	fmt.Println("daemon stopped")
	return nil
}
