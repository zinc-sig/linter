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
	// used counts every file byte under dir plus the bytes reserved by
	// uploads in progress: current entries, retired entries not yet
	// removed, and staging. Uploads reserve before writing, so the files
	// never exceed max.
	used int64
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
		c.remove([]*cacheEntry{e})
	}
}

// remove deletes retired entries no lint reads and returns their bytes
// to the budget.
func (c *assetCache) remove(gone []*cacheEntry) {
	for _, e := range gone {
		_ = os.RemoveAll(e.dir)
		c.mu.Lock()
		c.used -= e.size
		c.mu.Unlock()
	}
}

// reserve claims n bytes of the budget for an upload of key. When the
// budget is short it evicts the least recently used entries no lint is
// reading, key's own entry last, and removes them before returning, so
// the caller may write n bytes at once.
func (c *assetCache) reserve(key string, n int64) error {
	c.mu.Lock()
	if c.used+n <= c.max {
		c.used += n
		c.mu.Unlock()
		return nil
	}
	var candidates []string
	for k, e := range c.entries {
		if e.readers == 0 {
			candidates = append(candidates, k)
		}
	}
	slices.SortFunc(candidates, func(a, b string) int {
		switch {
		case a == key:
			return 1
		case b == key:
			return -1
		}
		return c.entries[a].lastUsed.Compare(c.entries[b].lastUsed)
	})
	var freed int64
	evict := 0
	for c.used-freed+n > c.max && evict < len(candidates) {
		freed += c.entries[candidates[evict]].size
		evict++
	}
	if c.used-freed+n > c.max {
		c.mu.Unlock()
		return errCacheFull
	}
	gone := make([]*cacheEntry, 0, evict)
	for _, k := range candidates[:evict] {
		gone = append(gone, c.retireLocked(k))
	}
	c.used += n
	c.mu.Unlock()
	c.remove(gone)
	return nil
}

// unreserve returns the bytes a failed upload reserved.
func (c *assetCache) unreserve(n int64) {
	c.mu.Lock()
	c.used -= n
	c.mu.Unlock()
}

// retireLocked takes key's entry out of the map. Its directory stays
// counted in used until it is removed.
func (c *assetCache) retireLocked(key string) *cacheEntry {
	e := c.entries[key]
	delete(c.entries, key)
	e.stale = true
	return e
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
	var reserved int64
	size, err := extractAssets(body, staging, c.max, func(n int64) error {
		if err := c.reserve(key, n); err != nil {
			return err
		}
		reserved += n
		return nil
	})
	if err == nil {
		err = c.install(key, fingerprint, staging, filepath.Join(c.dir, "set-"+randomHex(8)), size, reserved)
	}
	if err != nil {
		_ = os.RemoveAll(staging)
		c.unreserve(reserved)
	}
	return err
}

// install renames staging into place and swaps the entry; the reserved
// bytes become the entry's size.
func (c *assetCache) install(key, fingerprint, staging, final string, size, reserved int64) error {
	c.mu.Lock()
	if err := os.Rename(staging, final); err != nil {
		c.mu.Unlock()
		return diskError(err)
	}
	var gone []*cacheEntry
	if _, ok := c.entries[key]; ok {
		if e := c.retireLocked(key); e.readers == 0 {
			gone = append(gone, e)
		}
	}
	c.entries[key] = &cacheEntry{fingerprint: fingerprint, dir: final, size: size, lastUsed: time.Now()}
	c.used += size - reserved
	c.mu.Unlock()
	c.remove(gone)
	return nil
}

// diskError maps ENOSPC onto errCacheFull.
func diskError(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return errCacheFull
	}
	return err
}

// extractAssets unpacks a tar stream into dir under the rules of contract
// section 9.2 and returns the bytes written. reserve claims each file's
// declared size before the file is written.
func extractAssets(body io.Reader, dir string, maxBytes int64, reserve func(n int64) error) (int64, error) {
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
		if hdr.Size > maxBytes-total {
			return 0, errTooLarge
		}
		if err := reserve(hdr.Size); err != nil {
			return 0, err
		}
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
