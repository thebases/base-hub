package secretbox

// File overview:
// - Responsibility: one call per secret table that wires both re-encryption paths: the boot-time
//   migration of legacy zero-key values (orm.RegisterLegacyDataMigration, every migrated database)
//   and the `rotate-encrypt-key` CLI (modules.RegisterEncryptKeyRotation).
// - Key symbols: `RegisterSecretTable`.

import (
	"context"
	"fmt"
	"time"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// RegisterSecretTable registers the zero-key migration and the key rotation for the secret
// columns of table, encrypted with label. Call it from the owning models package's init().
func RegisterSecretTable(module, table, label string, columns ...string) {
	orm.RegisterLegacyDataMigration(module+": re-encrypt "+table, func(ctx context.Context, tx bun.IDB) error {
		key := AppKey()
		if key == "" {
			// Only CLI paths reach here without a key: the server refuses to start without one.
			log.Warnf("%s: zero-key re-encryption of %s skipped: app.encryptKey is not configured", module, table)
			return nil
		}
		result, err := ResealColumns(ctx, tx, table, columns, label, key, key)
		if err != nil {
			return err
		}
		if result.Resealed > 0 || result.Failed > 0 {
			log.Infof("%s: %s re-encrypted under app.encryptKey: values=%d undecryptable=%d", module, table, result.Resealed, result.Failed)
		}
		return nil
	})

	// Keyed per table: the rotation registry holds one function per name.
	kernelmodules.RegisterEncryptKeyRotation(module+":"+table, func(ctx context.Context, tx bun.IDB, oldKey, newKey string, _ time.Time) (string, error) {
		result, err := ResealColumns(ctx, tx, table, columns, label, oldKey, newKey)
		if err != nil {
			return "", err
		}
		if !result.Installed {
			return table + " not installed", nil
		}
		// An undecryptable value would stay readable by neither key: stop so the operator decides.
		if result.Failed > 0 {
			return "", fmt.Errorf("%s: %d value(s) cannot be decrypted with the current key (row ids logged)", table, result.Failed)
		}
		return fmt.Sprintf("%s values re-encrypted=%d", table, result.Resealed), nil
	})
}
