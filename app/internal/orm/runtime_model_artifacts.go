package orm

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/uptrace/bun"
)

type runtimeModelArtifactRow struct {
	HelperData string         `bun:"helper_data"`
	JsonDTO    map[string]any `bun:"json_dto"`
}

func runtimeStoredModelArtifacts(ctx context.Context, modelName string) (string, map[string]any, bool, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", nil, false, nil
	}

	db := runtimeMetadataDB(ctx)
	if db == nil {
		return "", nil, false, nil
	}

	var row runtimeModelArtifactRow
	err := db.NewSelect().
		TableExpr("? AS sys_model", bun.Ident("sys_model")).
		Column("helper_data", "json_dto").
		Where("name = ?", modelName).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, false, nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	return strings.TrimSpace(row.HelperData), row.JsonDTO, true, nil
}

func runtimeMetadataDB(ctx context.Context) bun.IDB {
	if env := EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	db := GetBunDB()
	if db == nil {
		return nil
	}
	return db
}
