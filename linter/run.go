package linter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Run executes l's native tool on files and returns the normalized Report.
// Errors are operational failures only (contract §3): the tool could not be
// executed, was killed by a signal, or produced output Parse could not
// understand.
func Run(l Linter, files []string) (Report, error) {
	argv := l.Command(files)
	cmd := exec.Command(argv[0], argv[1:]...)
	if e, ok := l.(Enver); ok {
		cmd.Env = envWithDefaults(e.Env())
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return Report{}, fmt.Errorf("%s: failed to execute: %w", argv[0], err)
		}
		exitCode = exitErr.ExitCode()
		if exitCode < 0 { // killed by a signal
			return Report{}, fmt.Errorf("%s: %s\nstderr: %s", argv[0], exitErr, StderrSnippet(stderr.Bytes()))
		}
	}

	report, err := l.Parse(stdout.Bytes(), stderr.Bytes(), exitCode)
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
