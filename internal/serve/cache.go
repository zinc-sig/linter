package serve

import (
	"archive/tar"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	cacheKeyRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	fingerprintRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Upload failures, mapped onto error codes by the handler (contract
// section 8.5).
var (
	errNotTar     = errors.New("the body is not a tar stream")
	errTooLarge   = errors.New("the asset set exceeds the cache budget")
	errCacheFull  = errors.New("the asset cache cannot make room")
	errUploadSlow = errors.New("the upload did not finish in time")
)

// invalidAssetsError is an upload the rules of contract section 9.2
// reject.
type invalidAssetsError struct{ reason string }

func (e *invalidAssetsError) Error() string { return e.reason }

func invalidAssets(format string, args ...any) error {
	return &invalidAssetsError{reason: fmt.Sprintf(format, args...)}
}

// assetCache holds one asset set per cache key on disk (contract section
// 9.2). Each stored set lives in its own directory, so a replacement
// never changes a directory a lint is copying from; a replaced or evicted
// directory is deleted when its last reader releases it.
type assetCache struct {
	dir string
	max int64

	mu      sync.Mutex
	entries map[string]*cacheEntry
	// keyLocks serialises uploads per key.
	keyLocks map[string]*keyLock
}

type cacheEntry struct {
	fingerprint string
	dir         string
	size        int64
	lastUsed    time.Time
	readers     int
	// stale entries are no longer in the map; the directory goes with
	// the last reader.
	stale bool
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

func newAssetCache(dir string, maxBytes int64) *assetCache {
	return &assetCache{
		dir:      dir,
		max:      maxBytes,
		entries:  make(map[string]*cacheEntry),
		keyLocks: make(map[string]*keyLock),
	}
}

// acquire returns the entry of key when it holds fingerprint, counted as
// read until release.
func (c *assetCache) acquire(key, fingerprint string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || e.fingerprint != fingerprint {
		return nil, false
	}
	e.readers++
	e.lastUsed = time.Now()
	return e, true
}

func (c *assetCache) release(e *cacheEntry) {
	c.mu.Lock()
	e.readers--
	remove := e.stale && e.readers == 0
	c.mu.Unlock()
	if remove {
		_ = os.RemoveAll(e.dir)
	}
}

func (c *assetCache) holds(key, fingerprint string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return ok && e.fingerprint == fingerprint
}

func (c *assetCache) lockKey(key string) func() {
	c.mu.Lock()
	l, ok := c.keyLocks[key]
	if !ok {
		l = &keyLock{}
		c.keyLocks[key] = l
	}
	l.refs++
	c.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		c.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(c.keyLocks, key)
		}
		c.mu.Unlock()
	}
}

// put stores body, a tar stream, as the asset set of key. It is a no-op
// when key already holds fingerprint.
func (c *assetCache) put(key, fingerprint string, body io.Reader) error {
	unlock := c.lockKey(key)
	defer unlock()
	if c.holds(key, fingerprint) {
		return nil
	}

	staging, err := os.MkdirTemp(c.dir, ".staging-")
	if err != nil {
		return diskError(err)
	}
	size, err := extractAssets(body, staging, c.max)
	if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}

	final := filepath.Join(c.dir, "set-"+randomHex(8))
	evicted, err := c.install(key, fingerprint, staging, final, size)
	if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	for _, dir := range evicted {
		_ = os.RemoveAll(dir)
	}
	return nil
}

// install makes room, renames staging into place and swaps the entry.
// It returns the directories that no reader holds any more.
func (c *assetCache) install(key, fingerprint, staging, final string, size int64) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var used int64
	var candidates []string
	for k, e := range c.entries {
		if k == key {
			continue // replaced below
		}
		used += e.size
		if e.readers == 0 {
			candidates = append(candidates, k)
		}
	}
	slices.SortFunc(candidates, func(a, b string) int {
		return c.entries[a].lastUsed.Compare(c.entries[b].lastUsed)
	})
	var evict []string
	for used+size > c.max && len(candidates) > 0 {
		k := candidates[0]
		candidates = candidates[1:]
		used -= c.entries[k].size
		evict = append(evict, k)
	}
	if used+size > c.max {
		return nil, errCacheFull
	}
	if err := os.Rename(staging, final); err != nil {
		return nil, diskError(err)
	}

	var gone []string
	retire := func(k string) {
		e := c.entries[k]
		delete(c.entries, k)
		e.stale = true
		if e.readers == 0 {
			gone = append(gone, e.dir)
		}
	}
	for _, k := range evict {
		retire(k)
	}
	if _, ok := c.entries[key]; ok {
		retire(key)
	}
	c.entries[key] = &cacheEntry{fingerprint: fingerprint, dir: final, size: size, lastUsed: time.Now()}
	return gone, nil
}

// diskError maps ENOSPC onto errCacheFull.
func diskError(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return errCacheFull
	}
	return err
}

// extractAssets unpacks a tar stream into dir under the rules of contract
// section 9.2 and returns the bytes written.
func extractAssets(body io.Reader, dir string, maxBytes int64) (int64, error) {
	tr := tar.NewReader(body)
	files := make(map[string]bool)
	dirs := make(map[string]bool) // true when an entry named it
	var total int64
	entries := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return 0, readError(err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if entries++; entries > maxAssetEntries {
			return 0, invalidAssets("more than %d entries", maxAssetEntries)
		}
		isDir := hdr.Typeflag == tar.TypeDir
		//nolint:staticcheck // TypeRegA is the legacy NUL type the contract accepts.
		if !isDir && hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return 0, invalidAssets("entry %q has unsupported type %q", hdr.Name, string(hdr.Typeflag))
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if isDir {
			name = strings.TrimSuffix(name, "/")
		}
		if err := checkAssetName(name); err != nil {
			return 0, invalidAssets("entry %q: %v", hdr.Name, err)
		}
		for p := path.Dir(name); p != "."; p = path.Dir(p) {
			if files[p] {
				return 0, invalidAssets("%q is used both as a file and as a directory", p)
			}
			if _, ok := dirs[p]; !ok {
				dirs[p] = false
			}
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if isDir {
			if files[name] || dirs[name] {
				return 0, invalidAssets("entry %q appears twice", name)
			}
			dirs[name] = true
			if err := os.MkdirAll(target, 0o755); err != nil {
				return 0, diskError(err)
			}
			continue
		}
		if _, used := dirs[name]; used || files[name] {
			return 0, invalidAssets("entry %q appears twice or is also a directory", name)
		}
		files[name] = true
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, diskError(err)
		}
		n, err := writeAssetFile(target, tr, maxBytes-total)
		if err != nil {
			return 0, err
		}
		total += n
	}
}

func writeAssetFile(target string, r io.Reader, room int64) (int64, error) {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, diskError(err)
	}
	n, err := io.Copy(f, io.LimitReader(r, room+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return 0, diskError(err)
		}
		return 0, readError(err)
	}
	if n > room {
		return 0, errTooLarge
	}
	return n, nil
}

// readError classifies an error from reading the upload body.
func readError(err error) error {
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return errUploadSlow
	case isMaxBytes(err):
		return errTooLarge
	case errors.Is(err, tar.ErrHeader), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, tar.ErrInsecurePath):
		return errNotTar
	case errors.Is(err, syscall.ENOSPC):
		return errCacheFull
	}
	return fmt.Errorf("%w: %v", errNotTar, err)
}

// checkAssetName applies the entry-name rules of contract section 9.2.
func checkAssetName(name string) error {
	switch {
	case name == "" || name == ".":
		return errors.New("empty name")
	case len(name) > maxAssetName:
		return fmt.Errorf("name longer than %d bytes", maxAssetName)
	case strings.HasPrefix(name, "/"):
		return errors.New("absolute name")
	case hasControl(name):
		return errors.New("control character in name")
	case path.Clean(name) != name:
		return errors.New("name is not clean")
	case slices.Contains(strings.Split(name, "/"), ".."):
		return errors.New("name has a .. segment")
	}
	return nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// copyAssets copies the regular files and directories under src into
// dst. Entries are typed by lstat, so a symbolic link is never followed.
func copyAssets(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.Mkdir(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return nil
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// emptyDir removes everything inside dir, keeping dir itself.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
