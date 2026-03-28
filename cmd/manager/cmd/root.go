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
	Use:   "funnel-manager",
	Short: "Funnel cluster coordinator",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().Int("port", 8080, "TCP port for API and worker communication")
	_ = viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port"))

	rootCmd.PersistentFlags().String("db-driver", "", "Database driver (mysql, postgres)")
	_ = viper.BindPFlag("db-driver", rootCmd.PersistentFlags().Lookup("db-driver"))

	rootCmd.PersistentFlags().String("db-dsn", "", "Database DSN")
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
