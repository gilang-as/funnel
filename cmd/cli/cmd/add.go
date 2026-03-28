package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/gilang/funnel/internal/daemon"
)

var addCmd = &cobra.Command{
	Use:   "add <magnet>",
	Short: "Add a torrent to the daemon",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdd,
}

func init() {
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	magnet := args[0]
	body, _ := json.Marshal(daemon.AddRequest{Magnet: magnet})

	resp, err := apiClient().Post(apiURL("/api/torrents"), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var e daemon.ErrorResponse
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("daemon error: %s", e.Error)
	}

	var r daemon.AddResponse
	json.NewDecoder(resp.Body).Decode(&r)
	if !r.New {
		fmt.Printf("Already exists [%s]: %s\n", r.Status, r.ID)
	} else {
		fmt.Printf("Added: %s\n", r.ID)
	}
	return nil
}
