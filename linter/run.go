package linter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultMaxOutput bounds each of a tool's stdout and stderr under
// RunContext when Options.MaxOutput is zero.
const DefaultMaxOutput = 16 << 20

// ErrOutputTooLarge reports a tool whose stdout or stderr exceeded the cap.
var ErrOutputTooLarge = errors.New("tool output exceeds the size limit")

// ToolEnv is the scrubbed base environment of a tool run under serve
// (contract section 9.3).
func ToolEnv() []string {
	return []string{
		"PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
	}
}

// ScrubbedEnv is ToolEnv plus l's Enver defaults: the whole environment a
// tool gets under serve. Nothing is inherited from the caller.
func ScrubbedEnv(l Linter) []string {
	env := ToolEnv()
	if e, ok := l.(Enver); ok {
		env = append(env, e.Env()...)
	}
	return env
}

// Options configures RunContext.
type Options struct {
	// Dir is the tool's working directory; empty means the caller's.
	Dir string
	// Env is the tool's whole environment. Nil means the caller's
	// environment plus l's Enver defaults for unset keys, as Run does.
	Env []string
	// MaxOutput bounds stdout and stderr each; zero means
	// DefaultMaxOutput.
	MaxOutput int
	// Redact, when set, rewrites stdout and stderr once the tool has
	// exited, before Parse or an error message sees them, so that no later
	// truncation can split what it removes.
	Redact func([]byte) []byte
}

// Run executes l's native tool on files and returns the normalized Report.
// Errors are operational failures only (contract §3): the tool could not be
// executed, was killed by a signal, or produced output Parse could not
// understand.
func Run(l Linter, files []string) (Report, error) {
	return RunContext(context.Background(), l, files, Options{})
}

// RunContext is Run with a context, a working directory and an explicit
// environment. The tool runs in its own process group with stdin from
// /dev/null. When ctx ends, the whole group is killed and RunContext
// returns ctx.Err() once the tool has exited.
func RunContext(ctx context.Context, l Linter, files []string, opts Options) (Report, error) {
	argv := l.Command(files)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	if cmd.Env == nil {
		if e, ok := l.(Enver); ok {
			cmd.Env = envWithDefaults(e.Env())
		}
	}
	setProcessGroup(cmd)
	// A descendant that escaped the group kill must not hold Wait open
	// through the pipes.
	cmd.WaitDelay = time.Second

	limit := opts.MaxOutput
	if limit <= 0 {
		limit = DefaultMaxOutput
	}
	stdout := &cappedBuffer{max: limit}
	stderr := &cappedBuffer{max: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	exitCode := 0
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	outBytes, errBytes := stdout.Bytes(), stderr.Bytes()
	if opts.Redact != nil {
		outBytes, errBytes = opts.Redact(outBytes), opts.Redact(errBytes)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return Report{}, fmt.Errorf("%s: failed to execute: %w", argv[0], runErr)
		}
		exitCode = exitErr.ExitCode()
		if exitCode < 0 { // killed by a signal
			return Report{}, fmt.Errorf("%s: %s\nstderr: %s", argv[0], exitErr, StderrSnippet(errBytes))
		}
	}
	if stdout.overflow || stderr.overflow {
		return Report{}, fmt.Errorf("%s: %w (%d bytes)", argv[0], ErrOutputTooLarge, limit)
	}

	report, err := l.Parse(outBytes, errBytes, exitCode)
	if err != nil {
		return Report{}, err
	}
	// Parsers report paths verbatim; map them back to the paths given on
	// the command line so findings match what the caller staged (§2).
	for i := range report.Findings {
		report.Findings[i].Path = NormalizePath(report.Findings[i].Path, files)
	}
	return report, nil
}

// cappedBuffer keeps the first max bytes written and records overflow.
// The buffer is a named field, not embedded, so that io.Copy cannot
// bypass Write through bytes.Buffer's ReadFrom.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); len(p) > room {
		b.overflow = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

// envWithDefaults returns the process environment plus any defaults whose
// keys are not already set.
func envWithDefaults(defaults []string) []string {
	env := os.Environ()
	for _, kv := range defaults {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, set := os.LookupEnv(key); !set {
			env = append(env, kv)
		}
	}
	return env
}

// NormalizePath maps a tool-reported path — often absolute like
// /workspace/solution.c, or prefixed like ./solution.go — back to the
// matching path from files, exactly as it was given to the CLI. Paths that
// match no given file are returned cleaned.
func NormalizePath(reported string, files []string) string {
	clean := filepath.Clean(reported)
	for _, f := range files {
		if filepath.Clean(f) == clean {
			return f
		}
	}
	// Suffix match at a path boundary, longest candidate first so nested
	// paths win over bare filenames.
	sorted := append([]string(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		return len(filepath.Clean(sorted[i])) > len(filepath.Clean(sorted[j]))
	})
	for _, f := range sorted {
		if strings.HasSuffix(clean, "/"+filepath.Clean(f)) {
			return f
		}
	}
	return clean
}

// StderrSnippet trims and truncates tool stderr for inclusion in
// operational-failure diagnostics.
func StderrSnippet(stderr []byte) string {
	s := strings.TrimSpace(string(stderr))
	const max = 2000
	if len(s) > max {
		s = s[:max] + " …(truncated)"
	}
	return s
}
