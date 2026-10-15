package checkstyleworker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const helperFlag = "-checkstyleworker-helper"

// TestMain turns the test binary into the fake JVM when the supervisor
// launches it with helperFlag.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperFlag {
		os.Exit(fakeWorker(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// fakeWorker speaks the Appendix A protocol. args are the mode and a state
// directory that counts starts. Requested file names script each reply:
//
//	sleep=<ms>  reply after a pause
//	crash       exit 1 at once
//	hang        never reply
//	garbage     write a line that is not a reply
//	error       error reply
//	unknown     a reply for an id never sent, then the real reply
//	big         a reply of 4 KiB
//	env         reply with the environment
//	active      reply with the most requests ever handled at once
func fakeWorker(args []string) int {
	mode, state := args[0], args[1]
	entries, _ := os.ReadDir(state)
	start := len(entries) + 1
	_ = os.WriteFile(filepath.Join(state, strconv.Itoa(start)), nil, 0o644)

	switch {
	case mode == "exit":
		return 1
	case mode == "hang":
		time.Sleep(time.Hour)
	case mode == "slow-restart" && start > 1:
		time.Sleep(time.Second)
	}

	var (
		outMu          sync.Mutex
		wg             sync.WaitGroup
		active, maxAct atomic.Int64
	)
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		b, _ := json.Marshal(v)
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = out.Write(append(b, '\n'))
		_ = out.Flush()
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(nil, 1<<20)
	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, "bad request:", err)
			return 3
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := active.Add(1)
			// Leave the active set before replying: the supervisor may
			// send the next request as soon as the reply lands.
			var leave sync.Once
			defer leave.Do(func() { active.Add(-1) })
			write := func(v any) {
				leave.Do(func() { active.Add(-1) })
				write(v)
			}
			for {
				m := maxAct.Load()
				if n <= m || maxAct.CompareAndSwap(m, n) {
					break
				}
			}
			file := req.Files[0]
			name := filepath.Base(file)
			xml := `<?xml version="1.0" encoding="UTF-8"?>` + "\n<checkstyle version=\"fake\">\n<file name=\"" + file + "\">\n</file>\n</checkstyle>\n"
			switch {
			case strings.HasPrefix(name, "sleep="):
				ms, _ := strconv.Atoi(strings.TrimPrefix(name, "sleep="))
				time.Sleep(time.Duration(ms) * time.Millisecond)
			case name == "crash":
				os.Exit(1)
			case name == "hang":
				time.Sleep(time.Hour)
			case name == "garbage":
				outMu.Lock()
				fmt.Fprintln(out, "Picked up JAVA_TOOL_OPTIONS")
				_ = out.Flush()
				outMu.Unlock()
				return
			case name == "error":
				write(map[string]any{"id": req.ID, "error": "boom"})
				return
			case name == "unknown":
				write(map[string]any{"id": req.ID + 1000, "xml": "stray"})
			case name == "big":
				xml = strings.Repeat("x", 4096)
			case name == "env":
				xml = strings.Join(os.Environ(), "\n")
			case name == "active":
				time.Sleep(100 * time.Millisecond)
				xml = strconv.FormatInt(maxAct.Load(), 10)
			}
			write(map[string]any{"id": req.ID, "xml": xml})
		}()
	}
	wg.Wait()
	return 0
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) log(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, msg)
}

func (l *logSink) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

type fixture struct {
	w     *Worker
	logs  *logSink
	state string
}

func (f *fixture) starts() int {
	entries, _ := os.ReadDir(f.state)
	return len(entries)
}

// start launches a Worker over the fake child and, unless the mode never
// warms up, waits for it to be ready.
func start(t *testing.T, mode string, mutate func(*Config)) *fixture {
	t.Helper()
	f := &fixture{logs: &logSink{}, state: t.TempDir()}
	cfg := Config{
		Threads:       4,
		Argv:          []string{os.Args[0], helperFlag, mode, f.state},
		Env:           []string{},
		WarmupFiles:   []string{"/warmup/Warmup.java"},
		WarmupTimeout: 5 * time.Second,
		Backoff:       []time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
		Log:           f.logs.log,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	w, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.w = w
	t.Cleanup(func() { _ = w.Close() })
	if mode == "exit" || mode == "hang" {
		return f
	}
	select {
	case <-w.Ready():
	case <-w.Failed():
		t.Fatalf("worker failed: %v", w.Err())
	case <-time.After(10 * time.Second):
		t.Fatal("worker not ready after 10s")
	}
	return f
}

func check(w *Worker, timeout time.Duration, file string) (Output, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return w.Check(ctx, []string{"/ws/" + file})
}

func TestCheckReturnsXMLAsToolOutput(t *testing.T) {
	f := start(t, "ok", nil)
	out, err := check(f.w, 5*time.Second, "Solution.java")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.ExitCode != 0 || out.Stderr != nil || !strings.Contains(string(out.Stdout), `<file name="/ws/Solution.java">`) {
		t.Fatalf("unexpected output %+v", out)
	}
}

func TestErrorReplyBecomesFailedToolOutput(t *testing.T) {
	f := start(t, "ok", nil)
	out, err := check(f.w, 5*time.Second, "error")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.ExitCode != 1 || string(out.Stderr) != "boom" || out.Stdout != nil {
		t.Fatalf("unexpected output %+v", out)
	}
}

func TestConcurrentRequestsGetTheirOwnReplies(t *testing.T) {
	f := start(t, "ok", nil)
	files := []string{"sleep=300", "sleep=200", "sleep=100", "sleep=0"}
	var wg sync.WaitGroup
	errs := make([]error, len(files))
	for i, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := check(f.w, 5*time.Second, file)
			switch {
			case err != nil:
				errs[i] = err
			case !strings.Contains(string(out.Stdout), `"/ws/`+file+`"`):
				errs[i] = fmt.Errorf("reply for %s is %q", file, out.Stdout)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestInFlightRequestsAreBoundedByThreads(t *testing.T) {
	f := start(t, "ok", func(c *Config) { c.Threads = 2 })
	var wg sync.WaitGroup
	results := make([]string, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := check(f.w, 5*time.Second, "active")
			if err != nil {
				results[i] = err.Error()
				return
			}
			results[i] = string(out.Stdout)
		}()
	}
	wg.Wait()
	for _, r := range results {
		if n, err := strconv.Atoi(r); err != nil || n > 2 {
			t.Errorf("max requests in flight in the child = %q, want at most 2", r)
		}
	}
}

func TestCrashFailsInFlightAndRestarts(t *testing.T) {
	f := start(t, "ok", nil)
	slow := make(chan error, 1)
	go func() {
		_, err := check(f.w, 5*time.Second, "sleep=2000")
		slow <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := check(f.w, 5*time.Second, "crash"); !errors.Is(err, ErrRestarted) {
		t.Fatalf("crashing request: err = %v, want ErrRestarted", err)
	}
	if err := <-slow; !errors.Is(err, ErrRestarted) {
		t.Fatalf("other request in flight: err = %v, want ErrRestarted", err)
	}
	if _, err := check(f.w, 5*time.Second, "Solution.java"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if got := f.starts(); got != 2 {
		t.Fatalf("starts = %d, want 2", got)
	}
}

func TestDeadlinePoisonsAndRestarts(t *testing.T) {
	f := start(t, "ok", nil)
	other := make(chan error, 1)
	go func() {
		_, err := check(f.w, 5*time.Second, "sleep=3000")
		other <- err
	}()
	time.Sleep(100 * time.Millisecond)
	began := time.Now()
	if _, err := check(f.w, 300*time.Millisecond, "hang"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("hanging request: err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("timed out after %s, want about 300ms", elapsed)
	}
	if err := <-other; !errors.Is(err, ErrRestarted) {
		t.Fatalf("other request in flight: err = %v, want ErrRestarted", err)
	}
	if _, err := check(f.w, 5*time.Second, "Solution.java"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if got := f.logs.count("poisoned"); got != 1 {
		t.Fatalf("poison log lines = %d, want 1", got)
	}
}

func TestExpiredRequestIsNotSent(t *testing.T) {
	f := start(t, "ok", nil)
	for range 20 {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		_, err := f.w.Check(ctx, []string{"/ws/Solution.java"})
		cancel()
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	}
	if got := f.logs.count("poisoned"); got != 0 {
		t.Fatalf("poison log lines = %d, want 0", got)
	}
	if got := f.starts(); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

func TestCancelledRequestWaitsForItsReply(t *testing.T) {
	f := start(t, "ok", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	began := time.Now()
	out, err := f.w.Check(ctx, []string{"/ws/sleep=400"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if elapsed := time.Since(began); elapsed < 350*time.Millisecond {
		t.Fatalf("returned after %s, before the reply", elapsed)
	}
	if out.ExitCode != 0 {
		t.Fatalf("unexpected output %+v", out)
	}
}

func TestCancelledAbandonedRequestIsPoisonedAtItsDeadline(t *testing.T) {
	f := start(t, "ok", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := f.w.Check(ctx, []string{"/ws/hang"}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestGarbageLineRestarts(t *testing.T) {
	f := start(t, "ok", nil)
	if _, err := check(f.w, 5*time.Second, "garbage"); !errors.Is(err, ErrRestarted) {
		t.Fatalf("err = %v, want ErrRestarted", err)
	}
	if _, err := check(f.w, 5*time.Second, "Solution.java"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if got := f.logs.count("invalid reply"); got != 1 {
		t.Fatalf("invalid reply log lines = %d, want 1", got)
	}
}

func TestOversizedReplyRestarts(t *testing.T) {
	f := start(t, "ok", func(c *Config) { c.MaxReplyBytes = 1024 })
	if _, err := check(f.w, 5*time.Second, "big"); !errors.Is(err, ErrRestarted) {
		t.Fatalf("err = %v, want ErrRestarted", err)
	}
}

func TestReplyForUnknownIDIsIgnored(t *testing.T) {
	f := start(t, "ok", nil)
	if _, err := check(f.w, 5*time.Second, "unknown"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := f.logs.count("unknown request"); got != 1 {
		t.Fatalf("unknown-id log lines = %d, want 1", got)
	}
	if got := f.starts(); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

func TestRequestDuringRestartTimesOutWithoutPoison(t *testing.T) {
	f := start(t, "slow-restart", nil)
	if _, err := check(f.w, 5*time.Second, "crash"); !errors.Is(err, ErrRestarted) {
		t.Fatalf("crash: err = %v, want ErrRestarted", err)
	}
	if _, err := check(f.w, 200*time.Millisecond, "Solution.java"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("during restart: err = %v, want ErrTimeout", err)
	}
	if _, err := check(f.w, 5*time.Second, "Solution.java"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if got := f.logs.count("poisoned"); got != 0 {
		t.Fatalf("poison log lines = %d, want 0", got)
	}
}

func TestConsecutiveFailedStartsAreTerminal(t *testing.T) {
	f := start(t, "exit", nil)
	select {
	case <-f.w.Failed():
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not fail")
	}
	if f.w.Err() == nil {
		t.Fatal("Err() = nil after failure")
	}
	if got := f.starts(); got != 3 {
		t.Fatalf("starts = %d, want 3", got)
	}
	select {
	case <-f.w.Ready():
		t.Fatal("Ready closed for a worker that never warmed up")
	default:
	}
	if _, err := check(f.w, time.Second, "Solution.java"); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestWarmupTimeoutCountsAsFailedStart(t *testing.T) {
	f := start(t, "hang", func(c *Config) {
		c.WarmupTimeout = 100 * time.Millisecond
		c.Backoff = []time.Duration{}
	})
	select {
	case <-f.w.Failed():
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not fail")
	}
	if err := f.w.Err(); err == nil || !strings.Contains(err.Error(), "no warm-up reply") {
		t.Fatalf("Err() = %v, want a warm-up timeout", err)
	}
}

func TestChildEnvironmentIsScrubbed(t *testing.T) {
	t.Setenv("ZINC_TEST_SECRET", "hunter2")
	f := start(t, "ok", func(c *Config) { c.Env = nil })
	out, err := check(f.w, 5*time.Second, "env")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := strings.Split(string(out.Stdout), "\n")
	want := DefaultEnv()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("child environment = %q, want %q", got, want)
	}
}

func TestCloseFailsInFlightRequests(t *testing.T) {
	f := start(t, "ok", nil)
	errc := make(chan error, 1)
	go func() {
		_, err := check(f.w, 5*time.Second, "hang")
		errc <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := f.w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	if _, err := check(f.w, time.Second, "Solution.java"); !errors.Is(err, ErrClosed) {
		t.Fatalf("after Close: err = %v, want ErrClosed", err)
	}
}

func TestCancelBeforeSendReturnsContextError(t *testing.T) {
	f := start(t, "hang", nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := f.w.Check(ctx, []string{"/ws/Solution.java"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
