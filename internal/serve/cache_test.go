package serve

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zinc-sig/linter/linter"
)

// listing prints every file of the workspace with its content.
const listing = `find . -type f | sort | while read -r f; do echo "$f=$(cat "$f")"; done`

func put(t *testing.T, ts *testServer, key, fp string, body []byte) response {
	t.Helper()
	return do(t, ts.client, http.MethodPut, "http://linter/v1/assets/"+key+"/"+fp, body, nil)
}

func messages(rep linter.Report) string {
	var m []string
	for _, f := range rep.Findings {
		m = append(m, f.Message)
	}
	return strings.Join(m, "\n")
}

func TestAssetFlow(t *testing.T) {
	ts := startServer(t, serverOpts{linters: []linter.Linter{fakeLinter{lang: "fake", script: listing}}})
	req := map[string]any{"language": "fake", "filename": "src/Main.java", "code": "student",
		"assets": map[string]string{"cache_key": "q1", "fingerprint": fp1}}

	r := ts.lint(t, req)
	if r.status != 409 || r.errorCode(t) != "assets_required" {
		t.Fatalf("lint before upload: %d %s", r.status, r.body)
	}
	archive := tarOf(t, dir("src/"), file("src/Main.java", "teacher"), file("./lib/Helper.java", "helper"), file("data.txt", "d"))
	if r := put(t, ts, "q1", fp1, archive); r.status != 204 || len(r.body) != 0 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	got := messages(ts.lint(t, req).report(t))
	want := "./data.txt=d\n./lib/Helper.java=helper\n./src/Main.java=student"
	if got != want {
		t.Errorf("workspace =\n%s\nwant\n%s", got, want)
	}

	// Same fingerprint again: 204, nothing read.
	if r := put(t, ts, "q1", fp1, []byte("not even a tar")); r.status != 204 {
		t.Errorf("repeat upload: %d %s", r.status, r.body)
	}
	// Another fingerprint is a miss until uploaded.
	req["assets"] = map[string]string{"cache_key": "q1", "fingerprint": fp2}
	if r := ts.lint(t, req); r.status != 409 {
		t.Errorf("new fingerprint: %d", r.status)
	}
	put(t, ts, "q1", fp2, tarOf(t, file("other.txt", "o")))
	if got := messages(ts.lint(t, req).report(t)); got != "./other.txt=o\n./src/Main.java=student" {
		t.Errorf("after replace:\n%s", got)
	}
	// An empty archive stores an empty set.
	if r := put(t, ts, "q2", fp1, tarOf(t)); r.status != 204 {
		t.Errorf("empty archive: %d %s", r.status, r.body)
	}
}

func TestAssetFileWhereDirectoryIsNeeded(t *testing.T) {
	ts := startServer(t, serverOpts{linters: []linter.Linter{fakeLinter{lang: "fake", script: listing}}})
	put(t, ts, "q1", fp1, tarOf(t, file("src", "a file")))
	r := ts.lint(t, map[string]any{"language": "fake", "filename": "src/Main.java", "code": "",
		"assets": map[string]string{"cache_key": "q1", "fingerprint": fp1}})
	if r.status != 500 || r.errorCode(t) != "workspace_setup_failed" {
		t.Errorf("got %d %s", r.status, r.body)
	}
}

func TestAssetUploadRejects(t *testing.T) {
	ts := startServer(t, serverOpts{tune: func(c *Config) { c.CacheMaxBytes = 4096 }})
	symlink := tar.Header{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "/etc/passwd"}
	hardlink := tar.Header{Typeflag: tar.TypeLink, Name: "hard", Linkname: "a"}
	fifo := tar.Header{Typeflag: tar.TypeFifo, Name: "fifo"}
	cases := []struct {
		name   string
		path   string
		body   []byte
		status int
		code   string
	}{
		{"symlink", "q/" + fp1, tarOf(t, symlink), 422, "assets_invalid"},
		{"hard link", "q/" + fp1, tarOf(t, file("a", "x"), hardlink), 422, "assets_invalid"},
		{"fifo", "q/" + fp1, tarOf(t, fifo), 422, "assets_invalid"},
		{"dotdot", "q/" + fp1, tarOf(t, file("../escape", "x")), 422, "assets_invalid"},
		{"absolute", "q/" + fp1, tarOf(t, file("/etc/x", "x")), 422, "assets_invalid"},
		{"unclean", "q/" + fp1, tarOf(t, file("a//b", "x")), 422, "assets_invalid"},
		{"duplicate", "q/" + fp1, tarOf(t, file("a", "x"), file("a", "y")), 422, "assets_invalid"},
		{"file then dir", "q/" + fp1, tarOf(t, file("a", "x"), file("a/b", "y")), 422, "assets_invalid"},
		{"dir then file", "q/" + fp1, tarOf(t, file("a/b", "x"), file("a", "y")), 422, "assets_invalid"},
		{"not tar", "q/" + fp1, []byte(strings.Repeat("garbage!", 100)), 400, "bad_request"},
		{"too large", "q/" + fp1, tarOf(t, file("big", strings.Repeat("x", 5000))), 413, "payload_too_large"},
		{"bad key", "-q/" + fp1, tarOf(t), 400, "bad_request"},
		{"bad fingerprint", "q/abc", tarOf(t), 400, "bad_request"},
		{"extra segment", "q/" + fp1 + "/x", tarOf(t), 400, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := do(t, ts.client, http.MethodPut, "http://linter/v1/assets/"+tc.path, tc.body, nil)
			if r.status != tc.status || r.errorCode(t) != tc.code {
				t.Errorf("got %d %s, want %d %s", r.status, r.body, tc.status, tc.code)
			}
		})
	}
	if r := do(t, ts.client, http.MethodPost, "http://linter/v1/assets/q/"+fp1, nil, nil); r.status != 405 || r.header.Get("Allow") != "PUT" {
		t.Errorf("POST to assets: %d %v", r.status, r.header)
	}
	// Failed uploads leave no staging directories behind.
	entries, _ := os.ReadDir(ts.cfg.CacheDir)
	if len(entries) != 0 {
		t.Errorf("cache directory not clean: %v", entries)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newAssetCache(t.TempDir(), 100)
	blob := func(n int) []byte { return tarOf(t, file("f", strings.Repeat("x", n))) }
	for _, k := range []string{"a", "b"} {
		if err := c.put(k, fp1, bytes.NewReader(blob(40))); err != nil {
			t.Fatal(err)
		}
	}
	// Touch a so that b is the least recently used.
	e, _ := c.acquire("a", fp1)
	c.release(e)
	if err := c.put("c", fp1, bytes.NewReader(blob(40))); err != nil {
		t.Fatal(err)
	}
	if c.holds("b", fp1) || !c.holds("a", fp1) || !c.holds("c", fp1) {
		t.Errorf("eviction picked the wrong entry: %v", keys(c))
	}
	// Entries being read cannot be evicted.
	ea, _ := c.acquire("a", fp1)
	ec, _ := c.acquire("c", fp1)
	if err := c.put("d", fp1, bytes.NewReader(blob(40))); !errors.Is(err, errCacheFull) {
		t.Errorf("put with every entry in use = %v, want errCacheFull", err)
	}
	c.release(ea)
	c.release(ec)
	entries, _ := os.ReadDir(c.dir)
	if len(entries) != 2 {
		t.Errorf("cache holds %d directories, want 2", len(entries))
	}
}

// diskWatcher feeds an upload body and records the most file bytes the
// cache directory held at any read.
type diskWatcher struct {
	r    io.Reader
	dir  string
	peak int64
}

func (d *diskWatcher) Read(p []byte) (int, error) {
	d.peak = max(d.peak, fileBytes(d.dir))
	return d.r.Read(p)
}

func fileBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func used(c *assetCache) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func TestCacheEvictsBeforeStaging(t *testing.T) {
	c := newAssetCache(t.TempDir(), 100)
	for _, k := range []string{"a", "b"} {
		if err := c.put(k, fp1, bytes.NewReader(tarOf(t, file("f", strings.Repeat("x", 40))))); err != nil {
			t.Fatal(err)
		}
	}
	// Two files, so that the second is read after the first was staged.
	body := &diskWatcher{r: bytes.NewReader(tarOf(t, file("f", strings.Repeat("x", 30)), file("g", strings.Repeat("x", 30)))), dir: c.dir}
	if err := c.put("c", fp1, body); err != nil {
		t.Fatal(err)
	}
	if body.peak > c.max {
		t.Errorf("the cache held %d bytes during the upload, over its budget of %d", body.peak, c.max)
	}
	if c.holds("a", fp1) || !c.holds("b", fp1) || !c.holds("c", fp1) {
		t.Errorf("eviction picked the wrong entry: %v", keys(c))
	}
	if got, want := used(c), fileBytes(c.dir); got != want {
		t.Errorf("used = %d, files hold %d", got, want)
	}
}

func TestCacheEvictsItsOwnKeyLast(t *testing.T) {
	c := newAssetCache(t.TempDir(), 100)
	c.put("k", fp1, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 30)))))
	c.put("o", fp1, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 30)))))
	// Touch o so that k is the least recently used.
	e, _ := c.acquire("o", fp1)
	c.release(e)
	if err := c.put("k", fp2, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 60))))); err != nil {
		t.Fatal(err)
	}
	if !c.holds("k", fp2) || c.holds("o", fp1) {
		t.Errorf("entries %v, want k replaced and o evicted first", keys(c))
	}
	// Room only by evicting k's own set: allowed while no lint reads it.
	if err := c.put("k", fp1, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 70))))); err != nil {
		t.Fatal(err)
	}
	if !c.holds("k", fp1) {
		t.Errorf("entries %v, want k replaced", keys(c))
	}
	if got, want := used(c), int64(70); got != want {
		t.Errorf("used = %d, want %d", got, want)
	}
}

func TestCacheFailedUploadReturnsItsReservation(t *testing.T) {
	c := newAssetCache(t.TempDir(), 100)
	c.put("k", fp1, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 30)))))
	old, _ := c.acquire("k", fp1)
	// The first file is staged, then the symbolic link rejects the upload.
	bad := tarOf(t, file("a", strings.Repeat("x", 20)), tar.Header{Typeflag: tar.TypeSymlink, Name: "l", Linkname: "/etc/passwd"})
	if err := c.put("k", fp2, bytes.NewReader(bad)); err == nil {
		t.Fatal("bad upload accepted")
	}
	if got := used(c); got != 30 {
		t.Errorf("used = %d after a failed upload, want 30", got)
	}
	// Room only by evicting k's own set, which a lint reads.
	if err := c.put("k", fp2, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 80))))); !errors.Is(err, errCacheFull) {
		t.Errorf("put = %v, want errCacheFull", err)
	}
	if !c.holds("k", fp1) {
		t.Error("a failed upload changed the entry")
	}
	// A replaced set still being read counts until its last reader leaves.
	if err := c.put("k", fp2, bytes.NewReader(tarOf(t, file("v", strings.Repeat("x", 50))))); err != nil {
		t.Fatal(err)
	}
	if got := used(c); got != 80 {
		t.Errorf("used = %d with the replaced set still read, want 80", got)
	}
	c.release(old)
	if got := used(c); got != 50 {
		t.Errorf("used = %d after the last reader left, want 50", got)
	}
}

func TestCacheReplaceKeepsReadersDirectory(t *testing.T) {
	c := newAssetCache(t.TempDir(), 1000)
	c.put("k", fp1, bytes.NewReader(tarOf(t, file("v", "one"))))
	old, ok := c.acquire("k", fp1)
	if !ok {
		t.Fatal("entry missing")
	}
	if err := c.put("k", fp2, bytes.NewReader(tarOf(t, file("v", "two")))); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(old.dir, "v")); err != nil || string(b) != "one" {
		t.Errorf("reader lost its set: %q %v", b, err)
	}
	c.release(old)
	if _, err := os.Stat(old.dir); !os.IsNotExist(err) {
		t.Error("replaced directory survived its last reader")
	}
	if !c.holds("k", fp2) || c.holds("k", fp1) {
		t.Error("replace did not swap the fingerprint")
	}
}

func TestCopyAssetsSkipsSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(src, "ok"), []byte("ok"), 0o644)
	os.Symlink(outside, filepath.Join(src, "link"))
	os.Symlink(filepath.Dir(outside), filepath.Join(src, "dirlink"))
	if err := copyAssets(src, dst); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 1 || entries[0].Name() != "ok" {
		t.Errorf("copied %v, want only ok", entries)
	}
}

func keys(c *assetCache) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var k []string
	for key := range c.entries {
		k = append(k, key)
	}
	return k
}
