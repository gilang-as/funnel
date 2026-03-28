package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time via:
//
//	go build -ldflags "-X github.com/gilang/funnel/cmd/cli/cmd.Version=v1.2.3"
//
// Defaults to "Development" when built without the flag.
var Version = "Development"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("funnel %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
