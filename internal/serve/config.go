package serve

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

// Protocol is the serve protocol version this package implements; it
// matches the /v1 path prefix (contract section 8.3).
const Protocol = 1

// Flag defaults (contract section 7.1).
const (
	DefaultSocket        = "/tmp/cobe-lint.sock"
	DefaultWorkers       = 4
	DefaultDeadline      = 8 * time.Second
	DefaultCacheDir      = "/workspace/lint-cache"
	DefaultCacheMaxBytes = 192 << 20
	DefaultWorkDir       = "/tmp"
)

// Fixed limits of protocol 1 (contract sections 8.1, 8.4 and 9).
const (
	MaxCodeBytes     = 1 << 20
	MaxFindings      = 1000
	maxLintBody      = 4 << 20
	maxHeaderBytes   = 64 << 10
	maxMessageBytes  = 4096
	maxResponseBytes = 8 << 20
	maxWorkers       = 64
	maxQueue         = 256
	maxAssetEntries  = 10000
	maxAssetName     = 4096
	minTokenBytes    = 32
	readHeaderWait   = 5 * time.Second
	idleTimeout      = 120 * time.Second
	uploadTimeout    = 60 * time.Second
	goWarmupTimeout  = 2 * time.Minute
	shutdownGrace    = 1500 * time.Millisecond
)

// TokenHeader carries the shared token on the TCP listener.
const TokenHeader = "X-Zinc-Linter-Token"

// Config is the parsed command line of `cobe-lint serve`.
type Config struct {
	Socket        string
	Listen        string
	TokenFile     string
	Workers       int
	Queue         int
	Deadline      time.Duration
	CacheDir      string
	CacheMaxBytes int64
	WorkDir       string
}

// errUsage marks a flag error, which exits 2.
var errUsage = errors.New("usage error")

// ParseFlags parses the serve flags. Errors wrap errUsage.
func ParseFlags(args []string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("cobe-lint serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c Config
	fs.StringVar(&c.Socket, "socket", DefaultSocket, "unix socket the server always listens on")
	fs.StringVar(&c.Listen, "listen", "", "optional TCP listen address (requires --token-file)")
	fs.StringVar(&c.TokenFile, "token-file", "", "file holding the TCP shared token (requires --listen)")
	fs.IntVar(&c.Workers, "workers", DefaultWorkers, "lints executing at once (1 to 64)")
	fs.IntVar(&c.Queue, "queue", -1, "lints that may wait for a worker (0 to 256; default --workers)")
	fs.DurationVar(&c.Deadline, "deadline", DefaultDeadline, "per-request lint deadline")
	fs.StringVar(&c.CacheDir, "cache-dir", DefaultCacheDir, "root of the asset cache")
	fs.Int64Var(&c.CacheMaxBytes, "cache-max-bytes", DefaultCacheMaxBytes, "byte budget of the asset cache")
	fs.StringVar(&c.WorkDir, "work-dir", DefaultWorkDir, "parent directory of the per-request workspaces")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("%w: unexpected argument %q", errUsage, fs.Arg(0))
	}
	if c.Queue == -1 {
		c.Queue = c.Workers
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%w: %v", errUsage, err)
	}
	return c, nil
}

func (c Config) validate() error {
	switch {
	case c.Socket == "":
		return errors.New("--socket must not be empty")
	case c.Listen != "" && c.TokenFile == "":
		return errors.New("--listen requires --token-file")
	case c.Listen == "" && c.TokenFile != "":
		return errors.New("--token-file is only valid with --listen")
	case c.Workers < 1 || c.Workers > maxWorkers:
		return fmt.Errorf("--workers %d is out of range (1 to %d)", c.Workers, maxWorkers)
	case c.Queue < 0 || c.Queue > maxQueue:
		return fmt.Errorf("--queue %d is out of range (0 to %d)", c.Queue, maxQueue)
	case c.Deadline <= 0:
		return errors.New("--deadline must be greater than zero")
	case c.CacheDir == "" || c.WorkDir == "":
		return errors.New("--cache-dir and --work-dir must not be empty")
	case c.CacheMaxBytes <= 0:
		return errors.New("--cache-max-bytes must be greater than zero")
	}
	return nil
}
