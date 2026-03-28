package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/gilang/funnel/internal/daemon"
)

var removeCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a torrent and delete its data",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	id := args[0]
	req, _ := http.NewRequest(http.MethodDelete, apiURL("/api/torrents/"+id), nil)

	resp, err := apiClient().Do(req)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Removed: %s\n", id)
		return nil
	}

	var e daemon.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&e)
	return fmt.Errorf("daemon error: %s", e.Error)
}
