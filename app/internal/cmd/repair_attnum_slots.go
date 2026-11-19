package cmd

// File overview:
// - Responsibility: implements a CLI command that reclaims Postgres attnum
//   slots a table has burned through repeated ADD/DROP COLUMN cycles —
//   Postgres never frees a dropped column's slot short of a full table
//   rewrite, so a table with a small live column count can still trip the
//   ORM's 1600-column safety guard (model_registry_migrate.go,
//   guardProjectedColumnCount) on attnum slot count alone.
// - Key symbols: `repairAttnumSlotsCmd`, `runRepairAttnumSlotsCommand`, `rewriteTableToReclaimAttnumSlots`.
// - Pattern: dry-run by default; requires --apply to mutate; refuses to run
//   if any foreign key touches the table, since this codebase otherwise
//   links models by logical model_name string rather than physical FKs, so
//   a real FK here is unexpected and the rewrite does not attempt to
//   recreate it automatically.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"
)

var (
	repairAttnumSlotsDBName string
	repairAttnumSlotsApply  bool
)

var repairAttnumSlotsCmd = &cobra.Command{
	Use:   "repair-attnum-slots <table>",
	Short: "Reclaim dropped-column attnum slots on a table (dry-run unless --apply)",
	Long: `Reports a table's live column count vs its true Postgres attnum slot
count (live + historically dropped columns, which Postgres never reclaims
short of a full table rewrite). With --apply, rewrites the table in place to
reset its attnum numbering, preserving data, indexes, defaults, and
constraints. Refuses to run if the table has any foreign key constraint,
inbound or outbound.`,
	Args: cobra.ExactArgs(1),
	RunE: runRepairAttnumSlotsCommand,
}

func init() {
	rootCmd.AddCommand(repairAttnumSlotsCmd)
	repairAttnumSlotsCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Path to config file")
	repairAttnumSlotsCmd.Flags().StringVarP(&repairAttnumSlotsDBName, "db", "d", "", "Logical database name to target (defaults to the home DB)")
	repairAttnumSlotsCmd.Flags().BoolVar(&repairAttnumSlotsApply, "apply", false, "Actually perform the rewrite (default is a dry-run report only)")
}

func runRepairAttnumSlotsCommand(cmd *cobra.Command, args []string) error {
	config.LoadConfig(configPath)

	table := strings.TrimSpace(args[0])
	if table == "" {
		return fmt.Errorf("repair-attnum-slots: table name is required")
	}

	logicalName := strings.TrimSpace(repairAttnumSlotsDBName)
	if logicalName == "" {
		logicalName = strings.TrimSpace(config.HomeDBName())
	}
	if logicalName == "" {
		return fmt.Errorf("repair-attnum-slots: database name is required")
	}
	dbCfg := config.Get().PostgresqlDb
	if namedCfg, ok := config.Get().PostgresqlDb.Databases[logicalName]; ok {
		dbCfg = namedCfg
	}
	dbCfg.Name = logicalName
	if strings.TrimSpace(dbCfg.Name) == "" {
		return fmt.Errorf("repair-attnum-slots: database %q is not configured", logicalName)
	}
	db, err := orm.NewBunDB(dbCfg)
	if err != nil {
		return fmt.Errorf("repair-attnum-slots: connect database %q: %w", logicalName, err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	before, err := queryAttnumSlotStats(ctx, db.DB, table)
	if err != nil {
		return err
	}
	fmt.Printf("table %q on database %s: %d live columns, %d total attnum slots (%d dropped)\n",
		table, logicalName, before.live, before.total, before.total-before.live)

	if !repairAttnumSlotsApply {
		fmt.Println("dry-run only — pass --apply to rewrite the table and reclaim the dropped slots")
		return nil
	}

	if err := rewriteTableToReclaimAttnumSlots(ctx, db.DB, table); err != nil {
		return err
	}

	after, err := queryAttnumSlotStats(ctx, db.DB, table)
	if err != nil {
		return err
	}
	fmt.Printf("rewrite complete: %d live columns, %d total attnum slots\n", after.live, after.total)
	fmt.Printf("original data preserved under %q — verify row counts/data, then DROP TABLE it once satisfied\n", rewrittenBackupTableName(table))
	return nil
}

type attnumSlotStats struct {
	live  int
	total int
}

// queryAttnumSlotStats reads the live column count and the true attnum slot
// count (live + dropped) pg_attribute has recorded for table.
func queryAttnumSlotStats(ctx context.Context, db *bun.DB, table string) (attnumSlotStats, error) {
	var stats attnumSlotStats
	err := db.NewRaw(
		`SELECT
			count(*) FILTER (WHERE NOT attisdropped) AS live,
			count(*) AS total
		FROM pg_attribute
		WHERE attrelid = ?::regclass AND attnum > 0`,
		table,
	).Scan(ctx, &stats.live, &stats.total)
	if err != nil {
		return attnumSlotStats{}, fmt.Errorf("repair-attnum-slots: inspect table %q: %w", table, err)
	}
	return stats, nil
}

// rewrittenBackupTableName is the fixed name the pre-rewrite table is
// renamed to; deterministic (not timestamped) so a retried --apply after a
// failed run still finds and reuses the same backup instead of piling up
// multiple *_slotrepair_old copies.
func rewrittenBackupTableName(table string) string {
	return table + "_slotrepair_old"
}

// rewriteTableToReclaimAttnumSlots resets table's attnum numbering by
// renaming it aside, creating a fresh table with the identical structure
// (LIKE ... INCLUDING ALL copies columns, defaults, indexes, and CHECK/
// unique/primary-key constraints, but never foreign keys), and copying every
// row across — all inside one transaction so the swap is atomic. The
// original table is preserved under rewrittenBackupTableName for manual
// verification rather than dropped automatically.
func rewriteTableToReclaimAttnumSlots(ctx context.Context, db *bun.DB, table string) error {
	var fkCount int
	if err := db.NewRaw(
		`SELECT count(*) FROM pg_constraint
		WHERE contype = 'f' AND (conrelid = ?::regclass OR confrelid = ?::regclass)`,
		table, table,
	).Scan(ctx, &fkCount); err != nil {
		return fmt.Errorf("repair-attnum-slots: check foreign keys on %q: %w", table, err)
	}
	if fkCount > 0 {
		return fmt.Errorf(
			"repair-attnum-slots: table %q has %d foreign key constraint(s) (inbound or outbound) — "+
				"this tool does not attempt to recreate them, resolve manually first",
			table, fkCount,
		)
	}

	backup := rewrittenBackupTableName(table)
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Exclusive lock for the duration of the tx: no concurrent reader/writer
		// can observe the table mid-swap, and no new column/FK can be added to
		// race against the fkCount check above.
		if _, err := tx.ExecContext(ctx, `LOCK TABLE ? IN ACCESS EXCLUSIVE MODE`, bun.Ident(table)); err != nil {
			return fmt.Errorf("repair-attnum-slots: lock %q: %w", table, err)
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS ?`, bun.Ident(backup)); err != nil {
			return fmt.Errorf("repair-attnum-slots: drop stale backup %q: %w", backup, err)
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE ? RENAME TO ?`, bun.Ident(table), bun.Ident(backup)); err != nil {
			return fmt.Errorf("repair-attnum-slots: rename %q to %q: %w", table, backup, err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE ? (LIKE ? INCLUDING ALL)`, bun.Ident(table), bun.Ident(backup)); err != nil {
			return fmt.Errorf("repair-attnum-slots: create fresh %q: %w", table, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ? SELECT * FROM ?`, bun.Ident(table), bun.Ident(backup)); err != nil {
			return fmt.Errorf("repair-attnum-slots: copy rows into fresh %q: %w", table, err)
		}
		return nil
	})
}
