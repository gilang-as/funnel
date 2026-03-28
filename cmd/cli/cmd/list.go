package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/gilang/funnel/internal/daemon"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List torrents in the daemon",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolP("downloading", "d", false, "show only downloading torrents")
	listCmd.Flags().BoolP("seeding", "s", false, "show only seeding torrents")
	listCmd.Flags().BoolP("paused", "p", false, "show only paused torrents")
	listCmd.Flags().BoolP("failed", "f", false, "show only failed torrents")
	listCmd.Flags().BoolP("queued", "q", false, "show only queued torrents")
}

func runList(cmd *cobra.Command, args []string) error {
	filter := statusFilter(cmd)

	u := apiURL("/api/torrents")
	if filter != "" {
		u += "?status=" + string(filter)
	}

	resp, err := apiClient().Get(u)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\nIs the daemon running? Try: funnel start", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response: %s", resp.Status)
	}

	var torrents []daemon.TorrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(torrents) == 0 {
		fmt.Println("No torrents.")
		return nil
	}

	fmt.Printf("%-10s  %-30s  %8s  %8s  %-11s  %5s\n",
		"ID", "NAME", "SIZE", "PROGRESS", "STATUS", "PEERS")
	fmt.Printf("%-10s  %-30s  %8s  %8s  %-11s  %5s\n",
		"----------", "------------------------------", "--------", "--------", "-----------", "-----")

	for _, t := range torrents {
		name := t.Name
		if len(name) > 30 {
			name = name[:27] + "..."
		}
		id := t.ID
		if len(id) > 10 {
			id = id[:10]
		}
		fmt.Printf("%-10s  %-30s  %8s  %7.1f%%  %-11s  %5d\n",
			id, name, formatBytes(t.Size), t.Progress, t.Status, t.Peers)
	}
	return nil
}

func statusFilter(cmd *cobra.Command) daemon.Status {
	flags := []struct {
		flag   string
		status daemon.Status
	}{
		{"downloading", daemon.StatusDownloading},
		{"seeding", daemon.StatusSeeding},
		{"paused", daemon.StatusPaused},
		{"failed", daemon.StatusFailed},
		{"queued", daemon.StatusQueued},
	}
	for _, f := range flags {
		if v, _ := cmd.Flags().GetBool(f.flag); v {
			return f.status
		}
	}
	return ""
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
