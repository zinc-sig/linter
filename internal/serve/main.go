package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"
)

// Main runs `cobe-lint serve` with args (the flags after "serve") and
// returns the exit code of contract section 7.3.
func Main(args []string, stderr io.Writer) int {
	cfg, err := ParseFlags(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "cobe-lint: serve: %v\n", err)
		return 2
	}
	// Tools run as the same user; a non-dumpable server keeps them out of
	// its /proc entries and memory.
	if err := setNonDumpable(); err != nil {
		fmt.Fprintf(stderr, "cobe-lint: serve: %v\n", err)
		return 1
	}
	s, err := New(cfg, DefaultDeps(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "cobe-lint: serve: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := s.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "cobe-lint: serve: %v\n", err)
		return 1
	}
	return 0
}
