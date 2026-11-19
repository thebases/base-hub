package filestore

// File overview:
// - Responsibility: checksum-addressed blob storage for sys.attachment (SRS-F-019) — one
//   content-addressed file tree per logical database under the attachment root.
// - Key symbols: `Store`, `Open`, `Remove`, `BlobPath`, `ListBlobs`, `RootDir`.
// - Owner decision (2026-07-08): `attachment.root` defaults to ./data/filestore with per-database
//   subdirectories; tenant DB drop keeps its filestore subtree (recoverable over reclaimed).
// - Pattern: Content addressing by SHA-256 makes writes idempotent and dedup automatic — two
//   uploads of identical bytes share one blob while their sys.attachment rows stay independent.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// defaultFilestoreRoot applies when `attachment.root` is absent from the home
// DB sys.config_param; relative paths resolve against the process working dir.
// A var, not a const, so tests can redirect blob writes to a scratch directory
// (same convention as the cron vacuum table vars).
var defaultFilestoreRoot = "./data/filestore"

const filestoreConfigParamKey = "attachment.root"

// RootDir resolves the filestore root from the home DB sys.config_param,
// falling back to the default when the param is absent or blank. Home DB only:
// blob placement is a control-plane concern and tenant DBs must not be able to
// redirect where other tenants' files land.
func RootDir(ctx context.Context) string {
	homeDBName := strings.TrimSpace(config.HomeDBName())
	homeDB := orm.ResolveRegisteredDatabase(homeDBName)
	if homeDB == nil || homeDB.DB == nil {
		return defaultFilestoreRoot
	}
	var raw string
	if err := homeDB.DB.NewRaw(
		`SELECT value::text FROM sys_config_param WHERE name = ? LIMIT 1`,
		filestoreConfigParamKey,
	).Scan(ctx, &raw); err != nil {
		return defaultFilestoreRoot
	}
	// jsonb text form quotes string values; strip before use.
	root := strings.Trim(strings.TrimSpace(raw), `"`)
	if root == "" {
		return defaultFilestoreRoot
	}
	return root
}

// databaseDir maps one logical database onto its filestore subtree. The name
// is sanitized so DB names with dots or slashes can never escape the root.
func databaseDir(root, dbName string) string {
	return filepath.Join(root, orm.SanitizeDBName(strings.TrimSpace(dbName)))
}

// BlobPath returns the content-addressed location for one checksum:
// <root>/<db>/<checksum[:2]>/<checksum>. The two-char fan-out keeps directory
// sizes bounded on large filestores.
func BlobPath(ctx context.Context, dbName, checksum string) (string, error) {
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	// A checksum shorter than the fan-out prefix (or empty) can never address a
	// stored blob and would otherwise build a path outside the per-DB tree.
	if len(checksum) != sha256.Size*2 {
		log.Errorf("filestore: invalid checksum %q", checksum)
		return "", fmt.Errorf("filestore: invalid checksum %q", checksum)
	}
	return filepath.Join(databaseDir(RootDir(ctx), dbName), checksum[:2], checksum), nil
}

// Store writes data as a content-addressed blob for one database and returns
// its checksum and size. Writing is idempotent: identical bytes land on the
// same path, so a pre-existing blob is dedup, not an error (SRS-F-019
// acceptance: two identical uploads share one blob).
func Store(ctx context.Context, dbName string, data []byte) (string, int64, error) {
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	path, err := BlobPath(ctx, dbName, checksum)
	if err != nil {
		return "", 0, err
	}
	// Dedup: the checksum IS the identity, so an existing file already holds
	// exactly these bytes and rewriting it would only churn mtimes the GC uses.
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
		return checksum, int64(len(data)), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", 0, fmt.Errorf("filestore: create blob directory: %w", err)
	}
	// Write to a temp file then rename: a crash mid-write must never leave a
	// half blob under the final checksum path, because readers trust the path
	// to contain exactly the checksummed bytes.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+checksum+".tmp-*")
	if err != nil {
		return "", 0, fmt.Errorf("filestore: create temp blob: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", 0, fmt.Errorf("filestore: write blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", 0, fmt.Errorf("filestore: finalize blob: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", 0, fmt.Errorf("filestore: publish blob: %w", err)
	}
	return checksum, int64(len(data)), nil
}

// Open returns the blob file for one checksum. Callers own closing it.
func Open(ctx context.Context, dbName, checksum string) (*os.File, error) {
	path, err := BlobPath(ctx, dbName, checksum)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// Remove deletes one blob. Only the attachment GC may call this: rows
// reference blobs by checksum with dedup, so deleting on row unlink would
// break the other rows sharing the content.
func Remove(ctx context.Context, dbName, checksum string) error {
	path, err := BlobPath(ctx, dbName, checksum)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Quarantine renames a corrupted blob aside so it stops serving while staying
// available for forensics (SRS-F-019 checksum-mismatch error contract).
func Quarantine(ctx context.Context, dbName, checksum string) error {
	path, err := BlobPath(ctx, dbName, checksum)
	if err != nil {
		return err
	}
	return os.Rename(path, path+".quarantine-"+time.Now().UTC().Format("20060102T150405Z"))
}

// BlobInfo describes one stored blob for garbage collection decisions.
type BlobInfo struct {
	Checksum string
	ModTime  time.Time
	Size     int64
}

// ListBlobs walks one database's filestore subtree and returns every stored
// blob. A missing subtree is an empty result, not an error — databases without
// attachments simply have no directory yet.
func ListBlobs(ctx context.Context, dbName string) ([]BlobInfo, error) {
	dir := databaseDir(RootDir(ctx), dbName)
	blobs := make([]BlobInfo, 0, 16)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		// Skip temp and quarantined files: they are not addressable blobs and the
		// GC must never treat them as unreferenced content.
		if len(name) != sha256.Size*2 || strings.Contains(name, ".") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		blobs = append(blobs, BlobInfo{Checksum: name, ModTime: info.ModTime(), Size: info.Size()})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return blobs, nil
}
