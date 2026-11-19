package models

// Mail server password at rest (plan T23).
//
// tbl.discuss_mail_server.password is sealed with base-credential's secretbox ("v1:" AES-GCM
// under a key derived from app.encryptKey, label = model name), like sys.credential. The default
// create/write seal a submitted plaintext through ModelSpec.Prepare; the sender opens it right
// before SMTP auth. Rows stored before this change hold plaintext: a boot migration seals them
// on every database, and the sender still accepts an unsealed value until that has run. The
// field is `meta:"secret"`, so client transports never return it (T19).

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

const (
	mailServerSecretLabel = DiscussMailServerModelName
	mailServerTable       = "tbl_discuss_mail_server"
)

// Test seam over the configured app.encryptKey.
var mailServerAppKey = secretbox.AppKey

func init() {
	// Registration order is run order: seal plaintext first, so the secretbox zero-key migration
	// registered next only ever sees "v1:" values.
	orm.RegisterLegacyDataMigration("discuss: seal plaintext mail server passwords", sealPlaintextMailServerPasswords)
	secretbox.RegisterSecretTable("discuss", mailServerTable, mailServerSecretLabel, "password")
}

// prepareMailServer seals a submitted plaintext password inside the guarded default create/write
// (ModelSpec.Prepare, plan T87): it runs after normalisation and before the insert/update of the
// default create (one record map; the model has no list-create override), the default write, and
// find_or_create/copy, so none of those RPC paths can store a plaintext password. Direct
// orm.RuntimeRepositoryCreate/Write callers bypass it.
func prepareMailServer(_ context.Context, op orm.WriteOp) error {
	return sealMailServerPasswordMap(op.Values)
}

func sealMailServerPasswordMap(values map[string]any) error {
	plaintext, ok := values["password"].(string)
	if !ok || strings.TrimSpace(plaintext) == "" {
		return nil // false/nil/empty clears the password as before
	}
	if secretbox.IsCurrent(plaintext) {
		// A client could otherwise store an arbitrary ciphertext; a password never starts with v1:.
		return orm.InvalidParamsf("%s: password must be sent in plain text", DiscussMailServerModelName)
	}
	sealed, err := secretbox.Seal(mailServerAppKey(), mailServerSecretLabel, plaintext)
	if err != nil {
		return err
	}
	values["password"] = sealed
	return nil
}

// openMailServerPassword returns the plaintext SMTP password for a stored value.
func openMailServerPassword(stored string) (string, error) {
	if !secretbox.IsCurrent(stored) {
		return stored, nil // legacy plaintext row, sealed by the boot migration on next start
	}
	return secretbox.Open(mailServerAppKey(), mailServerSecretLabel, stored)
}

// sealPlaintextMailServerPasswords seals every stored password that is not yet "v1:".
func sealPlaintextMailServerPasswords(ctx context.Context, tx bun.IDB) error {
	var exists bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", mailServerTable).Scan(ctx, &exists); err != nil || !exists {
		return err
	}
	key := mailServerAppKey()
	if key == "" {
		log.Warnf("discuss: mail server password sealing skipped: app.encryptKey is not configured")
		return nil
	}
	var rows []struct {
		ID       int64  `bun:"id"`
		Password string `bun:"password"`
	}
	if err := tx.NewRaw("SELECT id, password FROM ? WHERE password IS NOT NULL AND password <> '' AND password NOT LIKE 'v1:%'", bun.Ident(mailServerTable)).Scan(ctx, &rows); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, row := range rows {
		sealed, err := secretbox.Seal(key, mailServerSecretLabel, row.Password)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw("UPDATE ? SET password = ? WHERE id = ?", bun.Ident(mailServerTable), sealed, row.ID).Exec(ctx); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		log.Infof("discuss: sealed %d plaintext mail server password(s)", len(rows))
	}
	return nil
}
