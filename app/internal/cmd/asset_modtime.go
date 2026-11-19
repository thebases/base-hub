package cmd

// File overview:
// - Responsibility: gives embedded (go:embed) static assets a real modification time so HTTP revalidation works.
//   Also `sendRevalidatedAsset`: the ETag-based equivalent for module static resources, which are
//   read into memory rather than served through the static file server.
// - Key symbols: `withModTime`, `modTimeFS`, `modTimeFile`, `sendRevalidatedAsset`.
// - Pattern: wraps fs.FS/fs.File/fs.FileInfo only to override ModTime; every other call delegates to the embedded original.

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// embedFSModTime stands in for the build time of the embedded bundle. embed.FS
// reports a zero ModTime, so static.New sent "Last-Modified: 01 Jan 0001" and
// answered every If-Modified-Since with 304 — a browser revalidating (the SW's
// cache: "no-cache" refresh) kept an old .js module forever after a deploy.
// Process start is a safe proxy: a new binary always means a restart, and a
// restart without a new build only costs one 200 per asset. Truncated to the
// second because HTTP dates carry no sub-second precision; otherwise the
// comparison would never match and nothing could ever 304.
var embedFSModTime = time.Now().UTC().Truncate(time.Second)

// withModTime wraps an embedded filesystem so every file and directory reports
// embedFSModTime instead of the zero time.
func withModTime(fsys fs.FS) fs.FS {
	return modTimeFS{fsys: fsys, modTime: embedFSModTime}
}

type modTimeFS struct {
	fsys    fs.FS
	modTime time.Time
}

func (m modTimeFS) Open(name string) (fs.File, error) {
	f, err := m.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	return modTimeFile{File: f, modTime: m.modTime}, nil
}

// modTimeFile re-exposes the optional interfaces fasthttp's file server and
// sendStaticAsset probe for (io.Seeker, io.ReaderAt, fs.ReadDirFile); embed
// files implement all of them, so the wrapper must not hide them.
type modTimeFile struct {
	fs.File
	modTime time.Time
}

var errAssetCapability = errors.New("static asset: underlying file does not support this operation")

func (f modTimeFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return modTimeInfo{FileInfo: info, modTime: f.modTime}, nil
}

func (f modTimeFile) Seek(offset int64, whence int) (int64, error) {
	if s, ok := f.File.(io.Seeker); ok {
		return s.Seek(offset, whence)
	}
	return 0, errAssetCapability
}

func (f modTimeFile) ReadAt(p []byte, off int64) (int, error) {
	if r, ok := f.File.(io.ReaderAt); ok {
		return r.ReadAt(p, off)
	}
	return 0, errAssetCapability
}

func (f modTimeFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if d, ok := f.File.(fs.ReadDirFile); ok {
		return d.ReadDir(n)
	}
	return nil, errAssetCapability
}

type modTimeInfo struct {
	fs.FileInfo
	modTime time.Time
}

func (i modTimeInfo) ModTime() time.Time { return i.modTime }

// sendRevalidatedAsset serves an in-memory module static resource (/:module/static/*) so the browser
// asks again on every load: "no-cache" plus a content ETag. A versionless URL (dashboard.js) whose
// bytes change on deploy must never be reused blind — "max-age=3600" without a validator kept an old
// .js module for up to an hour while its freshly fetched .xml template already expected new methods.
// Unchanged files still cost only a bodiless 304.
func sendRevalidatedAsset(c fiber.Ctx, raw []byte) error {
	hash := fnv.New64a()
	_, _ = hash.Write(raw)
	etag := fmt.Sprintf(`"%x-%x"`, len(raw), hash.Sum64())
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set(fiber.HeaderETag, etag)
	if strings.Contains(c.Get(fiber.HeaderIfNoneMatch), etag) {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return c.Send(raw)
}
