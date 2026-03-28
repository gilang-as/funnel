package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"gopkg.gilang.dev/funnel/internal/daemon"
)

var pauseCmd = &cobra.Command{
	Use:   "pause <id>",
	Short: "Pause a torrent",
	Args:  cobra.ExactArgs(1),
	RunE:  runPause,
}

func init() {
	rootCmd.AddCommand(pauseCmd)
}

func runPause(cmd *cobra.Command, args []string) error {
	id := args[0]
	body, _ := json.Marshal(daemon.ActionRequest{Action: "pause"})
	req, _ := http.NewRequest(http.MethodPatch, apiURL("/api/torrents/"+id), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiClient().Do(req)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Paused: %s\n", id)
		return nil
	}

	var e daemon.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error == "" {
		return fmt.Errorf("daemon error (status %d)", resp.StatusCode)
	}
	return fmt.Errorf("daemon error: %s", e.Error)
}
