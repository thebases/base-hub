package cmd

// File overview:
// - Responsibility: CLI that rotates `app.encryptKey` — re-encrypts every ciphertext that modules
//   registered via modules.RegisterEncryptKeyRotation from the key in --config to the key in
//   --new-config, and purges any
//   sys.config_param copy of the key, one transaction per database.
// - Key symbols: `rotateEncryptKeyCmd`, `runRotateEncryptKeyCommand`, `readEncryptKeyFromConfig`.
// - Pattern: dry-run by default (runs everything, then rolls back, so counts and decryptability
//   are verified for real); --apply commits. Same --config/--db conventions as
//   reset-admin-password.
// - Key handling: both keys are read from YAML files (repo rule: no env vars) and never printed
//   or logged; the new key never appears on the command line or in shell history.
// - Runbook: stop the service, run with --apply, then start it on the new config. Running while
//   the service is live races its writes and expires codes it issues under the new key.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// minEncryptKeyLength rejects weak replacement keys (the leaked default is 16 hex chars).
const minEncryptKeyLength = 32

var (
	rotateEncryptKeyNewConfig string
	rotateEncryptKeyDBNames   []string
	rotateEncryptKeyApply     bool
)

var rotateEncryptKeyCmd = &cobra.Command{
	Use:   "rotate-encrypt-key",
	Short: "Re-encrypt module secrets under a new app.encryptKey (dry-run unless --apply)",
	Long: `Re-encrypts every module secret (each module registers its own rotation) from
app.encryptKey in --config (the current key) to app.encryptKey in --new-config (the
replacement), and deletes any sys.config_param copy of the key.

Stop the service first, run with --apply, then start the service with --new-config.
Pass every tenant database with --db (the default is the home database only): a
tenant left out keeps its secrets under the old key and cannot read them after the
switch.
Without --apply every step runs and is rolled back, reporting what would change.`,
	Args: cobra.NoArgs,
	RunE: runRotateEncryptKeyCommand,
}

func runRotateEncryptKeyCommand(cmd *cobra.Command, _ []string) error {
	config.LoadConfig(configPath)
	oldKey := strings.TrimSpace(config.Get().App.EncryptKey)
	newKey, err := readEncryptKeyFromConfig(rotateEncryptKeyNewConfig)
	if err != nil {
		return err
	}
	if err := validateEncryptKeyPair(oldKey, newKey); err != nil {
		return err
	}

	names := rotateEncryptKeyTargets()
	mode := "DRY-RUN (rolled back)"
	if rotateEncryptKeyApply {
		mode = "APPLIED"
	}
	now := time.Now().UTC()
	for index, name := range names {
		summary, err := rotateEncryptKeyOnDB(cmd.Context(), name, oldKey, newKey, now)
		if err != nil {
			// Earlier databases are already committed (with --apply); a rerun is safe for them
			// because ciphertexts already under the new key are skipped.
			return fmt.Errorf("rotate-encrypt-key: database %q failed (%d of %d done before it): %w", name, index, len(names), err)
		}
		fmt.Printf("%s %s: %s\n", mode, name, summary)
	}
	return nil
}

// rotateEncryptKeyTargets returns the --db list, defaulting to the home database.
func rotateEncryptKeyTargets() []string {
	names := make([]string, 0, len(rotateEncryptKeyDBNames))
	for _, name := range rotateEncryptKeyDBNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	if len(names) == 0 {
		names = append(names, strings.TrimSpace(config.HomeDBName()))
	}
	return names
}

// rotateEncryptKeyOnDB runs the rotation and the config-row purge in one transaction on one
// database, committing only with --apply.
func rotateEncryptKeyOnDB(parent context.Context, logicalName, oldKey, newKey string, now time.Time) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	dbCfg := config.Get().PostgresqlDb
	if namedCfg, ok := config.Get().PostgresqlDb.Databases[logicalName]; ok {
		dbCfg = namedCfg
	}
	dbCfg.Name = logicalName
	db, err := orm.NewBunDB(dbCfg)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	tx, err := db.DB.BeginTx(parent, nil)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	// Rolled back on any error and on dry-run; Rollback after Commit is a harmless no-op.
	defer func() { _ = tx.Rollback() }()

	// Each module re-encrypts its own ciphertext in the shared transaction.
	summaries := make([]string, 0)
	for _, entry := range modules.EncryptKeyRotations() {
		summary, err := entry.Rotate(parent, tx, oldKey, newKey, now)
		if err != nil {
			return "", fmt.Errorf("%s: %w", entry.Module, err)
		}
		summaries = append(summaries, entry.Module+": "+summary)
	}
	purged, err := config.PurgeFileOnlyConfigRows(parent, tx)
	if err != nil {
		return "", err
	}
	if rotateEncryptKeyApply {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit: %w", err)
		}
	}
	summaries = append(summaries, fmt.Sprintf("config rows purged=%d", purged))
	return strings.Join(summaries, "; "), nil
}

// readEncryptKeyFromConfig reads app.encryptKey (either spelling) from a YAML file without
// touching the process-wide config.
func readEncryptKeyFromConfig(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("rotate-encrypt-key: --new-config is required")
	}
	reader := viper.New()
	reader.SetConfigFile(path)
	if err := reader.ReadInConfig(); err != nil {
		return "", fmt.Errorf("rotate-encrypt-key: read --new-config: %w", err)
	}
	for _, key := range []string{"app.encryptkey", "app.encrypt_key"} {
		if value := strings.TrimSpace(reader.GetString(key)); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("rotate-encrypt-key: --new-config has no app.encryptKey")
}

// validateEncryptKeyPair refuses a rotation that would lose data or keep a weak key.
func validateEncryptKeyPair(oldKey, newKey string) error {
	switch {
	case oldKey == "":
		return fmt.Errorf("rotate-encrypt-key: --config has no app.encryptKey (the current key is required to decrypt)")
	case newKey == oldKey:
		return fmt.Errorf("rotate-encrypt-key: new key equals the current key")
	case len(newKey) < minEncryptKeyLength:
		return fmt.Errorf("rotate-encrypt-key: new key must be at least %d characters", minEncryptKeyLength)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(rotateEncryptKeyCmd)
	rotateEncryptKeyCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Config file holding the CURRENT app.encryptKey")
	rotateEncryptKeyCmd.Flags().StringVar(&rotateEncryptKeyNewConfig, "new-config", "", "Config file holding the NEW app.encryptKey")
	rotateEncryptKeyCmd.Flags().StringSliceVarP(&rotateEncryptKeyDBNames, "db", "d", nil, "Logical database(s) to rotate, repeatable (defaults to the home DB)")
	rotateEncryptKeyCmd.Flags().BoolVar(&rotateEncryptKeyApply, "apply", false, "Commit the rotation (default is a rolled-back dry run)")
}
