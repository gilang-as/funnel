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
	Use:   "funneld",
	Short: "Funnel standalone daemon (TCP-accessible)",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().Int("port", 8080, "TCP port for HTTP API")
	_ = viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port"))

	rootCmd.PersistentFlags().String("state", "file", "State store type (file, mysql, postgres)")
	_ = viper.BindPFlag("state", rootCmd.PersistentFlags().Lookup("state"))

	rootCmd.PersistentFlags().String("db-dsn", "", "Database DSN (e.g. user:pass@tcp(localhost:3306)/funnel)")
	_ = viper.BindPFlag("db-dsn", rootCmd.PersistentFlags().Lookup("db-dsn"))
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
