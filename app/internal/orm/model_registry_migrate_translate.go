package orm

// File overview:
// - Responsibility: owns the storage migration of translated fields (SRS-F-021) —
//   converting `translate:true` columns to jsonb with the `{"en": current}` wrap
//   and maintaining per-language expression indexes for search/order resolution.
// - Key symbols: `ensureTranslatedColumns`, `translatedColumnSet`.
// - Safety contract: the conversion is one-way by design (disabling translate
//   requires an explicit data migration per SRS-F-021 §5) and the generic schema
//   differ is exempted from these columns so it can never flip them back.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// translatedColumnSet lists the physical translated columns of one model so
// the schema differ can exempt them from type reconciliation.
func translatedColumnSet(metadata *ModelMetadata) map[string]struct{} {
	if metadata == nil {
		return nil
	}
	out := map[string]struct{}{}
	for _, field := range metadata.Fields {
		if !field.Translate {
			continue
		}
		if column := translatedFieldColumn(field); column != "" {
			out[column] = struct{}{}
		}
	}
	return out
}

// ensureTranslatedColumns migrates every translate:true column of the model to
// jsonb and provisions the per-language expression indexes. Enabling translate
// on an existing text column wraps current values as {"en": current} in place
// (SRS-F-021 §5); a column that is already jsonb is left untouched so reruns
// are idempotent.
func ensureTranslatedColumns(ctx context.Context, db *bun.DB, metadata *ModelMetadata) error {
	if db == nil || metadata == nil {
		return nil
	}
	columns := translatedColumnSet(metadata)
	if len(columns) == 0 {
		return nil
	}
	tableName := strings.TrimSpace(metadata.StorageName)
	if tableName == "" {
		return nil
	}

	langs := translatedIndexLangs(ctx, db)
	for column := range columns {
		dataType, err := lookupColumnDataType(ctx, db, tableName, column)
		if err != nil {
			return err
		}
		if dataType == "" {
			// Column missing entirely: the differ adds new columns before this
			// pass runs, so a miss means the model has no such storage yet.
			continue
		}
		if dataType != "jsonb" {
			// One-way conversion: wrap existing text as the en base value.
			// Blank strings collapse to NULL so the false-normalization of the
			// read path stays identical to the pre-migration behavior.
			if _, err := db.NewRaw(
				"ALTER TABLE ? ALTER COLUMN ? TYPE jsonb USING CASE WHEN ? IS NULL OR BTRIM(?::text) = '' THEN NULL ELSE jsonb_build_object('en', ?::text) END",
				bun.Ident(tableName), bun.Ident(column),
				bun.Ident(column), bun.Ident(column), bun.Ident(column),
			).Exec(ctx); err != nil {
				log.Errorf("orm: convert %s.%s to translated jsonb: %v", tableName, column, err)
				return fmt.Errorf("orm: convert %s.%s to translated jsonb: %w", tableName, column, err)
			}
			log.Infof("orm: converted %s.%s to translated jsonb (en-wrapped)", tableName, column)
		}
		// Per-language expression indexes back the COALESCE search/order
		// resolution (SRS-F-021 §4). Best effort: a failed index never blocks
		// boot because the queries stay correct without it, only slower.
		for _, lang := range langs {
			expr := translatedColumnSQL("", column, lang)
			indexName := translatedIndexName(tableName, column, lang)
			if _, err := db.NewRaw(
				"CREATE INDEX IF NOT EXISTS ? ON ? (("+expr+"))",
				bun.Ident(indexName), bun.Ident(tableName),
			).Exec(ctx); err != nil {
				log.Warnf("orm: create translated index %s on %s.%s: %v", indexName, tableName, column, err)
			}
		}
	}
	return nil
}

// lookupColumnDataType reads the current PostgreSQL data type of one column;
// an empty result means the column does not exist.
func lookupColumnDataType(ctx context.Context, db *bun.DB, tableName, column string) (string, error) {
	var dataType string
	err := db.NewSelect().
		TableExpr("information_schema.columns").
		ColumnExpr("data_type").
		Where("table_schema = current_schema()").
		Where("table_name = ?", tableName).
		Where("column_name = ?", column).
		Limit(1).
		Scan(ctx, &dataType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		log.Errorf("orm: inspect column %s.%s: %v", tableName, column, err)
		return "", fmt.Errorf("orm: inspect column %s.%s: %w", tableName, column, err)
	}
	return strings.ToLower(strings.TrimSpace(dataType)), nil
}

// translatedIndexLangs resolves the language codes worth indexing on this DB:
// the tbl_lang catalog when present, always including the en base. A missing
// or unseeded catalog (fresh bootstrap runs migrate before seeds) degrades to
// en-only; later boots pick up new languages because index creation reruns.
func translatedIndexLangs(ctx context.Context, db *bun.DB) []string {
	langs := []string{translationBaseLang}
	var codes []string
	if err := db.NewSelect().
		TableExpr("tbl_lang").
		ColumnExpr("code").
		Limit(50).
		Scan(ctx, &codes); err != nil {
		return langs
	}
	seen := map[string]struct{}{translationBaseLang: {}}
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" || !langCodePattern.MatchString(code) {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		langs = append(langs, code)
	}
	return langs
}

// translatedIndexName builds a deterministic, length-safe index name for one
// table/column/lang triple. PostgreSQL truncates identifiers past 63 bytes, so
// long combinations switch to a hash suffix to stay unique.
func translatedIndexName(tableName, column, lang string) string {
	name := fmt.Sprintf("idx_%s_%s_tr_%s", tableName, column, strings.ReplaceAll(lang, "-", "_"))
	if len(name) <= 63 {
		return name
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(name))
	return fmt.Sprintf("%s_%08x", name[:54], hasher.Sum32())
}
