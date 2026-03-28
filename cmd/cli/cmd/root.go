package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/gilang/funnel/internal/ipc"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:     "funnel",
	Short:   "Download torrents directly to S3 / local storage",
	Version: Version,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/funnel/config.yaml)")
	rootCmd.PersistentFlags().String("socket", "", "override IPC socket path")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if s, _ := cmd.Flags().GetString("socket"); s != "" {
			ipc.SetSocketPath(s)
		}
		return nil
	}
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := os.UserConfigDir()
		viper.AddConfigPath(home + "/funnel")
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("FUNNEL")
	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("storage.type", "local")
	viper.SetDefault("storage.local.dir", "~/Downloads/funnel")
	viper.SetDefault("storage.s3.region", "us-east-1")
	viper.SetDefault("storage.s3.base-dir", "./downloads")
	viper.SetDefault("upload-rate", 524288)
	viper.SetDefault("max-active", 3)

	_ = viper.ReadInConfig()
}
