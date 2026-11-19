// models/base.go
package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model base, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `BeforeCreate`, `BeforeUpdate`, `BaseModel`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"time"
)

// BaseModel is an embedded persistence fragment for shared identity and timestamps.
// It is intentionally abstract: embed it in concrete row structs instead of registering it as its own model/table.
type BaseModel struct {
	ID        int64     `bun:"id,pk,autoincrement" json:"id" meta:"readonly,primary"`
	Active    bool      `bun:"active" json:"active" default:"true" meta:"index,label:Active,help:Controls whether this record is active."`
	CreatedAt time.Time `bun:"created_at,nullzero" json:"created_at" meta:"readonly"`
	UpdatedAt time.Time `bun:"updated_at,nullzero" json:"updated_at" meta:"readonly"`
}

// MetadataBaseModel is an embedded persistence fragment for catalog rows that
// must keep shared identity and timestamps without inheriting archive semantics.
type MetadataBaseModel struct {
	ID        int64     `bun:"id,pk,autoincrement" json:"id" meta:"readonly,primary"`
	CreatedAt time.Time `bun:"created_at,nullzero" json:"created_at" meta:"readonly"`
	UpdatedAt time.Time `bun:"updated_at,nullzero" json:"updated_at" meta:"readonly"`
}

// BeforeCreate sets timestamps before inserting
func (b *BaseModel) BeforeCreate() {
	// Capture one UTC timestamp so CreatedAt and UpdatedAt stay consistent
	// within the same insert lifecycle.
	now := time.Now().UTC()
	// Preserve a caller-provided CreatedAt when fixtures or backfills set it.
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}

	// Always refresh UpdatedAt because every insert is also the first write.
	b.UpdatedAt = now
}

// BeforeCreate sets timestamps before inserting metadata rows.
func (b *MetadataBaseModel) BeforeCreate() {
	now := time.Now().UTC()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
}

// BeforeUpdate updates the UpdatedAt timestamp
func (b *BaseModel) BeforeUpdate() {
	// Only UpdatedAt changes on update; CreatedAt must remain immutable.
	b.UpdatedAt = time.Now().UTC()
}

// BeforeUpdate refreshes the metadata row update timestamp.
func (b *MetadataBaseModel) BeforeUpdate() {
	b.UpdatedAt = time.Now().UTC()
}

// ToggleActive flips the shared active flag for models that embed BaseModel.
func (b *BaseModel) ToggleActive() bool {
	b.Active = !b.Active
	return b.Active
}
