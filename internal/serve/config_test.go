package serve

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestParseFlagsDefaults(t *testing.T) {
	c, err := ParseFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Socket: DefaultSocket, Workers: 4, Queue: 4, Deadline: 8 * time.Second,
		CacheDir: DefaultCacheDir, CacheMaxBytes: 201326592, WorkDir: "/tmp"}
	if c != want {
		t.Errorf("defaults = %+v, want %+v", c, want)
	}
	c, err = ParseFlags([]string{"--workers", "6"}, io.Discard)
	if err != nil || c.Queue != 6 {
		t.Errorf("queue follows workers: %+v %v", c, err)
	}
	c, err = ParseFlags([]string{"--workers", "6", "--queue", "0"}, io.Discard)
	if err != nil || c.Queue != 0 {
		t.Errorf("explicit zero queue: %+v %v", c, err)
	}
}

func TestParseFlagsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus"},
		{"extra"},
		{"--listen", ":8080"},
		{"--token-file", "/t"},
		{"--workers", "0"},
		{"--workers", "65"},
		{"--queue", "257"},
		{"--queue", "-2"},
		{"--deadline", "0s"},
		{"--cache-max-bytes", "0"},
		{"--socket", ""},
	} {
		if _, err := ParseFlags(args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("ParseFlags(%v) = %v, want a usage error", args, err)
		}
	}
}

func TestMainUsageExitCode(t *testing.T) {
	if code := Main([]string{"--workers", "0"}, io.Discard); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
