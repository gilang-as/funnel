package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var autostartCmd = &cobra.Command{
	Use:   "autostart <enable|disable>",
	Short: "Configure funnel daemon to start automatically at login",
	Args:  cobra.ExactArgs(1),
	RunE:  runAutostart,
}

func init() {
	rootCmd.AddCommand(autostartCmd)
}

func runAutostart(cmd *cobra.Command, args []string) error {
	switch args[0] {
	case "enable":
		if err := doAutostart(true); err != nil {
			return err
		}
		fmt.Println("autostart enabled")
	case "disable":
		if err := doAutostart(false); err != nil {
			return err
		}
		fmt.Println("autostart disabled")
	default:
		return fmt.Errorf("expected 'enable' or 'disable', got %q", args[0])
	}
	return nil
}
