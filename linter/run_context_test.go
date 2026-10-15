//go:build unix

package linter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func noParse(t *testing.T) func([]byte, []byte, int) (Report, error) {
	return func([]byte, []byte, int) (Report, error) {
		t.Error("Parse must not be called")
		return Report{}, nil
	}
}

// TestRunContextKillsProcessGroup cancels a tool whose child outlives the
// shell's own work and checks that both are gone.
func TestRunContextKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	l := fake{
		argv:  []string{"sh", "-c", `sleep 60 & echo $$ $! > ` + pids + `; wait`},
		parse: noParse(t),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RunContext(ctx, l, nil, Options{Env: ToolEnv()})
		done <- err
	}()

	var fields []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, _ := os.ReadFile(pids)
		if fields = strings.Fields(string(b)); len(fields) == 2 {
			break
		}
	}
	if len(fields) != 2 {
		t.Fatal("the tool never wrote its pids")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("RunContext error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunContext did not return after cancellation")
	}
	for _, f := range fields {
		pid, _ := strconv.Atoi(f)
		// The shell is reaped by Wait; the orphaned sleep is reaped by init
		// or a subreaper, so poll briefly.
		gone := false
		for range 100 {
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
				gone = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !gone {
			t.Errorf("process %d survived the cancellation", pid)
		}
	}
}

func TestRunContextDeadline(t *testing.T) {
	l := fake{argv: []string{"sleep", "60"}, parse: noParse(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunContext(ctx, l, nil, Options{Env: ToolEnv()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the tool was not killed at the deadline")
	}
}

func TestRunContextExplicitEnvIsWhole(t *testing.T) {
	t.Setenv("COBE_LINT_SECRET", "s3cret")
	l := fakeWithEnv{
		fake: fake{argv: []string{"env"}},
		env:  []string{"COBE_LINT_DEFAULT=d"},
	}
	l.parse = func(stdout, _ []byte, _ int) (Report, error) {
		return Report{Tool: string(stdout), Findings: []Finding{}}, nil
	}
	report, err := RunContext(context.Background(), l, nil, Options{Env: ScrubbedEnv(l)})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	if strings.Contains(report.Tool, "s3cret") {
		t.Errorf("the inherited environment reached the tool:\n%s", report.Tool)
	}
	for _, want := range []string{"COBE_LINT_DEFAULT=d", "HOME=/tmp", "LANG=C.UTF-8"} {
		if !strings.Contains(report.Tool, want) {
			t.Errorf("tool environment lacks %s:\n%s", want, report.Tool)
		}
	}
}

func TestRunContextWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	l := fake{argv: []string{"pwd"}}
	l.parse = func(stdout, _ []byte, _ int) (Report, error) {
		return Report{Tool: strings.TrimSpace(string(stdout)), Findings: []Finding{}}, nil
	}
	report, err := RunContext(context.Background(), l, nil, Options{Dir: dir, Env: ToolEnv()})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(report.Tool); got != want {
		t.Errorf("working directory = %q, want %q", report.Tool, dir)
	}
}

func TestRunContextOutputCap(t *testing.T) {
	l := fake{argv: []string{"sh", "-c", "head -c 5000 /dev/zero"}, parse: noParse(t)}
	_, err := RunContext(context.Background(), l, nil, Options{Env: ToolEnv(), MaxOutput: 1000})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("error = %v, want ErrOutputTooLarge", err)
	}
}

func TestToolVersionIsMemoized(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	script := `echo x >> ` + counter + `; echo "tool version 1.2"`
	for range 3 {
		if got := ToolVersion("memo", `version (\S+)`, "sh", "-c", script); got != "memo 1.2" {
			t.Fatalf("ToolVersion = %q", got)
		}
	}
	b, _ := os.ReadFile(counter)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Errorf("probe ran %d times, want 1", n)
	}
}
