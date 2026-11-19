package cmd

// File overview:
// - Responsibility: implements a CLI command that resets the `admin` user's password in an existing database.
// - Key symbols: `resetAdminPasswordCmd`, `runResetAdminPasswordCommand`, `resetAdminUserPassword`.
// - Pattern: Reuses the runtime `base.users.write` path so password hashing and user-write invariants stay centralized.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"
)

var resetAdminPasswordDBName string

var resetAdminPasswordCmd = &cobra.Command{
	Use:   "reset-admin-password <password>",
	Short: "Reset the admin user's password in an existing database",
	Long: `Reset the admin user's password in an existing database.

The new password is required; there is no default.`,
	Args: cobra.ExactArgs(1),
	RunE: runResetAdminPasswordCommand,
}

func runResetAdminPasswordCommand(cmd *cobra.Command, args []string) error {
	config.LoadConfig(configPath)

	ctx := context.Background()
	logicalName := strings.TrimSpace(resetAdminPasswordDBName)
	if logicalName == "" {
		logicalName = strings.TrimSpace(config.HomeDBName())
	}
	password, err := resolvedResetAdminPasswordValue(args)
	if err != nil {
		return err
	}

	if logicalName == "" {
		return fmt.Errorf("reset-admin-password: database name is required")
	}
	dbCfg := config.Get().PostgresqlDb
	if namedCfg, ok := config.Get().PostgresqlDb.Databases[logicalName]; ok {
		dbCfg = namedCfg
	}
	dbCfg.Name = logicalName
	if strings.TrimSpace(dbCfg.Name) == "" {
		return fmt.Errorf("reset-admin-password: database %q is not configured", logicalName)
	}
	db, err := orm.NewBunDB(dbCfg)
	if err != nil {
		return fmt.Errorf("reset-admin-password: connect database %q: %w", logicalName, err)
	}
	orm.SetNamedBunDB(logicalName, db)
	defer func() {
		orm.SetNamedBunDB(logicalName, nil)
		_ = db.Close()
	}()

	// Register runtime models only after the target database is live so module
	// bootstraps can resolve the same storage the command is about to mutate.
	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	// Run the update as sudo so the CLI does not depend on a pre-authenticated
	// session and still exercises the model's centralized password logic.
	env := orm.NewEnvironmentWithDBName(db.DB, logicalName, 0, nil, true)
	resetCtx := orm.ContextWithEnvironment(ctx, env)
	if err := resetAdminUserPassword(resetCtx, db.DB, password); err != nil {
		return err
	}

	fmt.Printf("admin password reset on database %s\n", logicalName)
	return nil
}

// resolvedResetAdminPasswordValue returns the operator-supplied password. A built-in default
// would leave every reset admin on a guessable credential, so a missing or blank value fails.
func resolvedResetAdminPasswordValue(args []string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("reset-admin-password: a non-empty password argument is required")
	}
	return strings.TrimSpace(args[0]), nil
}

func resetAdminUserPassword(ctx context.Context, db *bun.DB, password string) error {
	// Validate the target row up front so the command reports a clear operator
	// error instead of a generic runtime write failure later in the flow.
	if db == nil {
		return fmt.Errorf("reset-admin-password: database is not initialized")
	}
	var userID int64
	err := db.NewRaw(
		`SELECT id FROM base_users WHERE BTRIM(login) = ? ORDER BY id ASC LIMIT 1`,
		"admin",
	).Scan(ctx, &userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("reset-admin-password: admin user not found")
		}
		return fmt.Errorf("reset-admin-password: query admin user: %w", err)
	}
	_, err = orm.ExecuteRuntimeMethod(
		ctx,
		models.ResUserModelName,
		"write",
		[]string{fmt.Sprintf("%d", userID)},
		[]any{orm.RecordMap{"new_password": password}},
		nil,
	)
	if err != nil {
		return fmt.Errorf("reset-admin-password: update admin user: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(resetAdminPasswordCmd)
	resetAdminPasswordCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Path to config file")
	resetAdminPasswordCmd.Flags().StringVarP(&resetAdminPasswordDBName, "db", "d", "", "Logical database name to update (defaults to the home DB)")
}
