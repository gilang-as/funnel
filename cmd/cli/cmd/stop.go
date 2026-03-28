package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/gilang/funnel/internal/daemon"
)

var stopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop seeding a torrent (data retained)",
	Args:  cobra.ExactArgs(1),
	RunE:  runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	id := args[0]
	req, _ := http.NewRequest(http.MethodPost, apiURL("/api/torrents/"+id+"/stop"), nil)

	resp, err := apiClient().Do(req)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Stopped seeding: %s (data retained)\n", id)
		return nil
	}

	var e daemon.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&e)
	return fmt.Errorf("daemon error: %s", e.Error)
}
