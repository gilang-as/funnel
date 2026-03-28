package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "funnel-worker",
	Short: "Funnel cluster worker node",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().String("manager", "http://localhost:8080", "Manager URL")
	_ = viper.BindPFlag("manager", rootCmd.PersistentFlags().Lookup("manager"))

	rootCmd.PersistentFlags().String("token", "", "Cluster join token (or set FUNNEL_JOIN_TOKEN)")
	_ = viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))

	rootCmd.PersistentFlags().Int("capacity", 3, "Max concurrent downloads")
	_ = viper.BindPFlag("capacity", rootCmd.PersistentFlags().Lookup("capacity"))

	// Storage flags
	rootCmd.PersistentFlags().String("storage-type", "local", "Storage type (local, s3)")
	_ = viper.BindPFlag("storage.type", rootCmd.PersistentFlags().Lookup("storage-type"))

	rootCmd.PersistentFlags().String("local-dir", "~/Downloads/funnel", "Local storage directory")
	_ = viper.BindPFlag("storage.local.dir", rootCmd.PersistentFlags().Lookup("local-dir"))

	rootCmd.PersistentFlags().String("s3-bucket", "", "S3 bucket name")
	_ = viper.BindPFlag("storage.s3.bucket", rootCmd.PersistentFlags().Lookup("s3-bucket"))

	rootCmd.PersistentFlags().String("s3-endpoint", "", "S3 endpoint URL")
	_ = viper.BindPFlag("storage.s3.endpoint", rootCmd.PersistentFlags().Lookup("s3-endpoint"))

	rootCmd.PersistentFlags().String("s3-access-key", "", "S3 access key")
	_ = viper.BindPFlag("storage.s3.access-key", rootCmd.PersistentFlags().Lookup("s3-access-key"))

	rootCmd.PersistentFlags().String("s3-secret-key", "", "S3 secret key")
	_ = viper.BindPFlag("storage.s3.secret-key", rootCmd.PersistentFlags().Lookup("s3-secret-key"))

	rootCmd.PersistentFlags().String("s3-region", "us-east-1", "S3 region")
	_ = viper.BindPFlag("storage.s3.region", rootCmd.PersistentFlags().Lookup("s3-region"))

	rootCmd.PersistentFlags().String("s3-base-dir", "downloads", "S3 base directory")
	_ = viper.BindPFlag("storage.s3.base-dir", rootCmd.PersistentFlags().Lookup("s3-base-dir"))

	rootCmd.PersistentFlags().Int("upload-rate", 0, "Upload rate limit (bytes/sec)")
	_ = viper.BindPFlag("upload-rate", rootCmd.PersistentFlags().Lookup("upload-rate"))
}

func initConfig() {
	home, err := os.UserHomeDir()
	cobra.CheckErr(err)

	viper.AddConfigPath(filepath.Join(home, ".config", "funnel"))
	viper.SetConfigType("yaml")
	viper.SetConfigName("config")

	viper.SetEnvPrefix("FUNNEL")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		// config loaded
	}
}
