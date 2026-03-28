package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"gopkg.gilang.dev/funnel/internal/cluster"
	"gopkg.gilang.dev/funnel/internal/store"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage cluster join tokens",
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new join token",
	RunE:  runTokenCreate,
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all join tokens",
	RunE:  runTokenList,
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke a join token",
	Args:  cobra.ExactArgs(1),
	RunE:  runTokenRevoke,
}

func init() {
	rootCmd.AddCommand(tokenCmd)
	tokenCmd.AddCommand(tokenCreateCmd)
	tokenCmd.AddCommand(tokenListCmd)
	tokenCmd.AddCommand(tokenRevokeCmd)

	tokenCreateCmd.Flags().String("name", "", "Human-readable name for the token")
}

func runTokenCreate(cmd *cobra.Command, args []string) error {
	s, err := initStore()
	if err != nil {
		return err
	}
	defer s.Close()

	name, _ := cmd.Flags().GetString("name")
	raw, err := cluster.GenerateToken()
	if err != nil {
		return err
	}

	hash := cluster.HashToken(raw)
	id := uuid.New().String()

	t := &store.JoinToken{
		ID:        id,
		TokenHash: hash,
		Name:      name,
		Revoked:   false,
	}

	if err := s.Tokens().Create(context.Background(), t); err != nil {
		return err
	}

	fmt.Println("New join token created!")
	fmt.Printf("ID:    %s\n", id)
	fmt.Printf("Token: %s\n", raw)
	fmt.Println("\nIMPORTANT: Store this token securely. It will not be shown again.")
	return nil
}

func runTokenList(cmd *cobra.Command, args []string) error {
	s, err := initStore()
	if err != nil {
		return err
	}
	defer s.Close()

	tokens, err := s.Tokens().List(context.Background())
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tCREATED\tSTATUS")
	for _, t := range tokens {
		status := "active"
		if t.Revoked {
			status = "revoked"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Format("2006-01-02 15:04"), status)
	}
	w.Flush()
	return nil
}

func runTokenRevoke(cmd *cobra.Command, args []string) error {
	s, err := initStore()
	if err != nil {
		return err
	}
	defer s.Close()

	id := args[0]
	if err := s.Tokens().Revoke(context.Background(), id); err != nil {
		return err
	}

	fmt.Printf("Token %s revoked.\n", id)
	return nil
}

func initStore() (store.Store, error) {
	driver := viper.GetString("db-driver")
	dsn := viper.GetString("db-dsn")
	if driver == "" || dsn == "" {
		return nil, fmt.Errorf("--db-driver and --db-dsn are required")
	}

	switch driver {
	case "mysql":
		return store.NewMySQLStore(dsn)
	case "postgres":
		return store.NewPostgresStore(dsn)
	default:
		return nil, fmt.Errorf("unsupported db-driver: %s", driver)
	}
}
