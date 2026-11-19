package modules

// File overview:
// - Responsibility: inbound hook letting a module take part in `rotate-encrypt-key`
//   (the app.encryptKey rotation CLI) without internal/ importing the module.
// - Key symbols: `EncryptKeyRotation`, `RegisterEncryptKeyRotation`, `EncryptKeyRotations`.
// - Pattern: a module that stores ciphertext under app.encryptKey registers a function in
//   its package init(); the CLI runs every registered rotation inside its per-database
//   transaction, so a dry run rolls all of them back together.

import (
	"context"
	"github.com/uptrace/bun"
	"sort"
	"sync"
	"time"
)

// EncryptKeyRotation re-encrypts a module's ciphertext from oldKey to newKey inside tx and
// returns a one-line summary for the operator. It must be idempotent: values already under
// newKey are skipped so a rerun after a partial --apply is safe. A module that is not
// installed on the database returns a "not installed" summary and no error.
type EncryptKeyRotation func(ctx context.Context, tx bun.IDB, oldKey, newKey string, now time.Time) (string, error)

var (
	encryptKeyRotationMu sync.RWMutex
	encryptKeyRotations  = map[string]EncryptKeyRotation{}
)

// RegisterEncryptKeyRotation registers the rotation for the named module.
func RegisterEncryptKeyRotation(module string, fn EncryptKeyRotation) {
	if module == "" || fn == nil {
		return
	}
	encryptKeyRotationMu.Lock()
	defer encryptKeyRotationMu.Unlock()
	encryptKeyRotations[module] = fn
}

// EncryptKeyRotationEntry is one registered rotation, returned in a stable order.
type EncryptKeyRotationEntry struct {
	Module string
	Rotate EncryptKeyRotation
}

// EncryptKeyRotations returns the registered rotations sorted by module name.
func EncryptKeyRotations() []EncryptKeyRotationEntry {
	encryptKeyRotationMu.RLock()
	defer encryptKeyRotationMu.RUnlock()
	out := make([]EncryptKeyRotationEntry, 0, len(encryptKeyRotations))
	for module, fn := range encryptKeyRotations {
		out = append(out, EncryptKeyRotationEntry{Module: module, Rotate: fn})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out
}
