package secretbox

// File overview:
// - Responsibility: re-encrypts every secret column of one table in place, for the boot-time
//   zero-key migration and for `rotate-encrypt-key`.
// - Key symbols: `ResealColumns`, `ResealResult`.
// - Safety: runs inside the caller's transaction; never logs a secret, only row ids and columns.
//   updated_at is left alone: re-encryption does not change the secret.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ResealResult counts what ResealColumns did. Installed is false when the table does not exist.
type ResealResult struct {
	Installed bool
	Resealed  int
	Failed    int
}

// ResealColumns re-encrypts each non-empty value of columns in table from oldKey (or the legacy
// zero key) to newKey. A value that cannot be opened is counted in Failed and left as it is.
func ResealColumns(ctx context.Context, db bun.IDB, table string, columns []string, label, oldKey, newKey string) (ResealResult, error) {
	var result ResealResult
	if _, err := deriveKey(newKey, label); err != nil {
		return result, err
	}
	var exists bool
	if err := db.NewRaw("SELECT to_regclass(?) IS NOT NULL", table).Scan(ctx, &exists); err != nil {
		return result, fmt.Errorf("secretbox: check table %s: %w", table, err)
	}
	if !exists {
		return result, nil
	}
	result.Installed = true

	query := db.NewSelect().TableExpr("?", bun.Ident(table)).Column("id")
	for _, column := range columns {
		query = query.ColumnExpr("COALESCE(?, '') AS ?", bun.Ident(column), bun.Ident(column))
	}
	var rows []map[string]any
	// Lock the rows so a concurrent password change or token refresh is not overwritten with the
	// re-encrypted old value.
	if err := query.OrderExpr("id").For("UPDATE").Scan(ctx, &rows); err != nil {
		return result, fmt.Errorf("secretbox: read %s: %w", table, err)
	}

	for _, row := range rows {
		update := db.NewUpdate().TableExpr("?", bun.Ident(table)).Where("id = ?", row["id"])
		changes := 0
		for _, column := range columns {
			value := columnString(row[column])
			resealed, changed, err := Reseal(oldKey, newKey, label, value)
			if err != nil {
				result.Failed++
				log.Errorf("secretbox: %s id=%v column=%s cannot be decrypted, left unchanged: %v", table, row["id"], column, err)
				continue
			}
			if changed {
				update = update.Set("? = ?", bun.Ident(column), resealed)
				changes++
			}
		}
		if changes == 0 {
			continue
		}
		if _, err := update.Exec(ctx); err != nil {
			return result, fmt.Errorf("secretbox: update %s id=%v: %w", table, row["id"], err)
		}
		result.Resealed += changes
	}
	return result, nil
}

func columnString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}
