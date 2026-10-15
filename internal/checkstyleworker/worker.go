// Package checkstyleworker runs and supervises the resident checkstyle JVM
// that serves Java lints under `cobe-lint serve` (docs/CONTRACT.md section
// 9.6 and Appendix A). The JVM is a local child process that speaks JSON
// lines over its stdin and stdout; a Worker starts it, warms it up,
// restarts it when it dies or overruns a deadline, and hands each lint the
// checkstyle XML the one-shot CLI would print, so the checkstyle driver's
// Parse is reused unchanged.
//
// Callers pass Check a context that carries the request deadline. Once a
// request has been written to the JVM, Check ignores cancellation and
// returns only when the JVM replies, dies, or the deadline passes, because
// the JVM cannot stop the thread that runs the audit. When the deadline
// passes first, Check returns ErrTimeout and the request keeps its thread
// slot until the JVM replies. A request still unanswered RunBudget after it
// was sent poisons the JVM, which is then killed and restarted. The budget
// is measured from the send, so time spent waiting for a slot or a restart
// never counts against the JVM.
package checkstyleworker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Paths of the worker's parts inside the image (Dockerfile runtime stage).
const (
	JavaPath      = "/opt/java/bin/java"
	CheckstyleJar = "/opt/checkstyle.jar"
	WorkerJar     = "/opt/checkstyle-worker.jar"
	ConfigPath    = "/opt/checkstyle-config.xml"
	WarmupFile    = "/opt/checkstyle-warmup/Warmup.java"
)

// DefaultArgv is the JVM launch line of Appendix A for a pool of threads
// audit threads.
func DefaultArgv(threads int) []string {
	return []string{
		JavaPath, "-XX:+UseSerialGC", "-Xmx128m", "-XX:-UsePerfData",
		"-cp", CheckstyleJar + ":" + WorkerJar, "CheckstyleWorker",
		"--config", ConfigPath, "--threads", strconv.Itoa(threads),
	}
}

// DefaultEnv is the scrubbed tool environment of contract section 9.3.
func DefaultEnv() []string {
	return []string{
		"PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
	}
}

// Errors Check returns besides a context error.
var (
	// ErrTimeout: the deadline passed, either while waiting for a slot or
	// a running JVM (nothing was sent) or with the request in flight (its
	// reply is discarded on arrival), or the run budget passed (the JVM was
	// poisoned and has been killed). Serve answers 504 timeout.
	ErrTimeout = errors.New("checkstyle worker: deadline exceeded")
	// ErrRestarted: the JVM died or was replaced with the request in
	// flight. Serve answers 503 linter_not_ready.
	ErrRestarted = errors.New("checkstyle worker: restarted while the request was in flight")
	// ErrFailed: the JVM failed to start too many times in a row; the
	// worker does not recover. Serve answers 503 linter_not_ready and
	// readiness reports failed.
	ErrFailed = errors.New("checkstyle worker: failed to start")
	// ErrClosed: Close was called.
	ErrClosed = errors.New("checkstyle worker: closed")
)

// Config configures a Worker. Only Threads is required; the rest default
// to the image layout and the contract's values.
type Config struct {
	// Threads is the JVM audit pool size and the bound on requests in
	// flight (serve's --workers).
	Threads int
	// Argv is the child command; defaults to DefaultArgv(Threads).
	Argv []string
	// Env is the child's whole environment; defaults to DefaultEnv. The
	// server's own environment is never inherited.
	Env []string
	// Dir is the child's working directory; defaults to "/".
	Dir string
	// WarmupFiles is the warm-up request; defaults to WarmupFile.
	WarmupFiles []string
	// WarmupTimeout bounds one start: launch plus the warm-up reply.
	// Defaults to 60 seconds.
	WarmupTimeout time.Duration
	// Backoff is the pause after each consecutive failed start; its length
	// is the number of failed starts tolerated, and one more failed start
	// marks the worker failed. Defaults to 1 s and 2 s (three starts).
	Backoff []time.Duration
	// RunBudget is how long the JVM may take over one request, measured
	// from the send; a request still unanswered after it poisons the JVM.
	// Defaults to 8 seconds.
	RunBudget time.Duration
	// MaxReplyBytes bounds one reply line; a longer line counts as an
	// invalid reply. Defaults to 32 MiB.
	MaxReplyBytes int
	// Log receives supervisor events and the child's stderr, one line per
	// call. Defaults to writing to os.Stderr.
	Log func(msg string)
}

// Output is a reply in the shape of a one-shot tool run, ready for the
// checkstyle driver's Parse: a success reply is (xml, nil, 0), an error
// reply is (nil, message, 1).
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Worker supervises the resident checkstyle JVM. It is safe for
// concurrent use.
type Worker struct {
	cfg  Config
	sem  chan struct{}
	quit chan struct{}
	done chan struct{}

	mu       sync.Mutex
	current  *process      // warmed up and serving, nil while (re)starting
	starting *process      // being launched or warmed up
	changed  chan struct{} // closed and replaced when current changes
	ready    chan struct{} // closed on the first successful warm-up
	failed   chan struct{} // closed on terminal failure
	failErr  error
	closed   bool
	gen      int
}

// Start validates cfg and starts the supervisor, which launches and warms
// up the JVM in the background; watch Ready and Failed for the outcome.
func Start(cfg Config) (*Worker, error) {
	if cfg.Threads < 1 {
		return nil, fmt.Errorf("checkstyle worker: threads must be at least 1, got %d", cfg.Threads)
	}
	if cfg.Argv == nil {
		cfg.Argv = DefaultArgv(cfg.Threads)
	}
	if cfg.Env == nil {
		cfg.Env = DefaultEnv()
	}
	if cfg.Dir == "" {
		cfg.Dir = "/"
	}
	if cfg.WarmupFiles == nil {
		cfg.WarmupFiles = []string{WarmupFile}
	}
	if cfg.WarmupTimeout <= 0 {
		cfg.WarmupTimeout = 60 * time.Second
	}
	if cfg.Backoff == nil {
		cfg.Backoff = []time.Duration{time.Second, 2 * time.Second}
	}
	if cfg.RunBudget <= 0 {
		cfg.RunBudget = 8 * time.Second
	}
	if cfg.MaxReplyBytes <= 0 {
		cfg.MaxReplyBytes = 32 << 20
	}
	if cfg.Log == nil {
		cfg.Log = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	w := &Worker{
		cfg:     cfg,
		sem:     make(chan struct{}, cfg.Threads),
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
		changed: make(chan struct{}),
		ready:   make(chan struct{}),
		failed:  make(chan struct{}),
	}
	go w.supervise()
	return w, nil
}

// Ready is closed once the first JVM answered its warm-up. It stays
// closed through later restarts.
func (w *Worker) Ready() <-chan struct{} { return w.ready }

// Failed is closed when the worker gave up after consecutive failed
// starts; Err then reports why.
func (w *Worker) Failed() <-chan struct{} { return w.failed }

// Err is the terminal failure, or nil.
func (w *Worker) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failErr
}

// Close stops the supervisor and kills the JVM; requests in flight return
// ErrClosed. It waits for the JVM to exit.
func (w *Worker) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		<-w.done
		return nil
	}
	w.closed = true
	close(w.quit)
	for _, p := range []*process{w.current, w.starting} {
		if p != nil {
			p.kill("closing")
		}
	}
	w.mu.Unlock()
	<-w.done
	return nil
}

// Check lints files (absolute paths) on the resident JVM. See the package
// documentation for how the context's deadline and cancellation apply.
func (w *Worker) Check(ctx context.Context, files []string) (Output, error) {
	if len(files) == 0 {
		return Output{}, errors.New("checkstyle worker: no files")
	}
	select {
	case w.sem <- struct{}{}:
	case <-ctx.Done():
		return Output{}, ctxErr(ctx)
	case <-w.quit:
		return Output{}, ErrClosed
	}
	held := true
	defer func() {
		if held {
			<-w.sem
		}
	}()

	for {
		p, err := w.await(ctx)
		if err != nil {
			return Output{}, err
		}
		// select picks at random among ready cases, so an expired request
		// can get here; sending it would only waste a JVM thread.
		if ctx.Err() != nil {
			return Output{}, ctxErr(ctx)
		}
		reply, sent := p.send(files)
		if !sent {
			continue // died between await and send; wait for the next one
		}
		budget := time.NewTimer(w.cfg.RunBudget)
		out, settled, err := w.wait(ctx, p, reply, budget)
		if settled {
			budget.Stop()
		} else {
			held = false
			go w.finish(p, reply, budget)
		}
		return out, err
	}
}

// await returns the serving JVM, waiting through a restart.
func (w *Worker) await(ctx context.Context) (*process, error) {
	for {
		w.mu.Lock()
		p, changed, closed, failErr := w.current, w.changed, w.closed, w.failErr
		w.mu.Unlock()
		switch {
		case closed:
			return nil, ErrClosed
		case failErr != nil:
			return nil, ErrFailed
		case p != nil:
			return p, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctxErr(ctx)
		}
	}
}

// wait collects the reply of a request already sent to p. Cancellation
// without a deadline is ignored here (package documentation). settled is
// false when the deadline passed first: the request is still on the JVM
// and its slot and budget pass to finish.
func (w *Worker) wait(ctx context.Context, p *process, reply <-chan result, budget *time.Timer) (out Output, settled bool, err error) {
	var deadline <-chan time.Time
	if d, ok := ctx.Deadline(); ok {
		t := time.NewTimer(time.Until(d))
		defer t.Stop()
		deadline = t.C
	}
	select {
	case r := <-reply:
		out, err = w.output(r)
		return out, true, err
	case <-budget.C:
		if r, ok := w.poison(p, reply); ok {
			out, err = w.output(r)
			return out, true, err
		}
		return Output{}, true, ErrTimeout
	case <-deadline:
	}
	select {
	case r := <-reply: // the reply raced the deadline
		out, err = w.output(r)
		return out, true, err
	default:
		return Output{}, false, ErrTimeout
	}
}

// finish holds the thread slot of a request whose caller has been
// answered until the JVM replies or the run budget passes.
func (w *Worker) finish(p *process, reply <-chan result, budget *time.Timer) {
	defer func() { <-w.sem }()
	select {
	case <-reply: // discarded
		budget.Stop()
	case <-budget.C:
		_, _ = w.poison(p, reply)
	}
}

// poison kills p for a request that overran its run budget and waits for
// p to exit. When the reply raced the budget, it returns the reply instead.
func (w *Worker) poison(p *process, reply <-chan result) (result, bool) {
	select {
	case r := <-reply:
		return r, true
	default:
	}
	p.kill("request exceeded its run budget (poisoned)")
	<-p.done
	return result{}, false
}

func (w *Worker) output(r result) (Output, error) {
	switch {
	case r.err != nil:
		w.mu.Lock()
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return Output{}, ErrClosed
		}
		return Output{}, r.err
	case r.errMsg != nil:
		return Output{Stderr: []byte(*r.errMsg), ExitCode: 1}, nil
	default:
		return Output{Stdout: []byte(r.xml), ExitCode: 0}, nil
	}
}

func ctxErr(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	return ctx.Err()
}

// supervise keeps one warmed-up JVM serving until Close or terminal
// failure.
func (w *Worker) supervise() {
	defer close(w.done)
	failures := 0
	for {
		p, err := w.launch()
		if err != nil {
			if errors.Is(err, ErrClosed) {
				return
			}
			failures++
			w.cfg.Log(fmt.Sprintf("checkstyle worker: start %d failed: %v", failures, err))
			if failures > len(w.cfg.Backoff) {
				w.mu.Lock()
				w.failErr = fmt.Errorf("checkstyle worker failed to start %d times in a row: %w", failures, err)
				close(w.failed)
				w.broadcastLocked()
				w.mu.Unlock()
				return
			}
			select {
			case <-time.After(w.cfg.Backoff[failures-1]):
			case <-w.quit:
				return
			}
			continue
		}
		failures = 0

		w.mu.Lock()
		w.starting = nil
		if w.closed {
			w.mu.Unlock()
			p.kill("closing")
			<-p.done
			return
		}
		w.current = p
		select {
		case <-w.ready:
		default:
			close(w.ready)
		}
		w.broadcastLocked()
		w.mu.Unlock()
		w.cfg.Log(fmt.Sprintf("checkstyle worker %d: ready (pid %d)", p.gen, p.pid))

		<-p.done

		w.mu.Lock()
		w.current = nil
		w.broadcastLocked()
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return
		}
		w.cfg.Log(fmt.Sprintf("checkstyle worker %d: exited (%v); restarting", p.gen, p.cause()))
	}
}

func (w *Worker) broadcastLocked() {
	close(w.changed)
	w.changed = make(chan struct{})
}

// launch starts one JVM and waits for its warm-up reply.
func (w *Worker) launch() (*process, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, ErrClosed
	}
	w.gen++
	gen := w.gen
	w.mu.Unlock()

	p, err := spawn(w.cfg, gen)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		p.kill("closing")
		<-p.done
		return nil, ErrClosed
	}
	w.starting = p
	w.mu.Unlock()

	fail := func(err error) (*process, error) {
		p.kill("warm-up failed")
		<-p.done
		w.mu.Lock()
		w.starting = nil
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		return nil, err
	}
	reply, sent := p.send(w.cfg.WarmupFiles)
	if !sent {
		return fail(fmt.Errorf("exited before warm-up: %v", p.cause()))
	}
	timer := time.NewTimer(w.cfg.WarmupTimeout)
	defer timer.Stop()
	select {
	case r := <-reply:
		switch {
		case r.err != nil:
			return fail(fmt.Errorf("exited during warm-up: %v", p.cause()))
		case r.errMsg != nil:
			return fail(fmt.Errorf("warm-up lint failed: %s", *r.errMsg))
		case !bytes.Contains([]byte(r.xml), []byte("</checkstyle>")):
			return fail(errors.New("warm-up reply holds no checkstyle report"))
		}
		return p, nil
	case <-timer.C:
		return fail(fmt.Errorf("no warm-up reply within %s", w.cfg.WarmupTimeout))
	}
}

// result is one decoded reply, or err when the JVM died first.
type result struct {
	xml    string
	errMsg *string
	err    error
}

// process is one JVM child.
type process struct {
	gen  int
	pid  int
	cmd  *exec.Cmd
	log  func(string)
	done chan struct{} // closed once reaped and every pending request failed

	writeMu sync.Mutex
	stdin   io.WriteCloser

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan result
	dead    bool   // no new requests; set on kill or reader exit
	reaped  bool   // Wait returned; the pid may be reused
	reason  string // why it was killed, if it was
	exitErr error
}

func spawn(cfg Config, gen int) (*process, error) {
	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = time.Second
	p := &process{
		gen:     gen,
		cmd:     cmd,
		log:     cfg.Log,
		done:    make(chan struct{}),
		pending: map[uint64]chan result{},
	}
	cmd.Stderr = &lineLogger{prefix: fmt.Sprintf("checkstyle worker %d: ", gen), log: cfg.Log}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launch %s: %w", cfg.Argv[0], err)
	}
	p.stdin = stdin
	p.pid = cmd.Process.Pid
	go p.read(stdout, cfg.MaxReplyBytes)
	return p, nil
}

type request struct {
	ID    uint64   `json:"id"`
	Files []string `json:"files"`
}

type replyLine struct {
	ID    uint64  `json:"id"`
	XML   *string `json:"xml"`
	Error *string `json:"error"`
}

// send writes one request; sent is false when the process is already dead.
func (p *process) send(files []string) (<-chan result, bool) {
	ch := make(chan result, 1)
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return nil, false
	}
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.mu.Unlock()

	line, err := json.Marshal(request{ID: id, Files: files})
	if err != nil {
		panic(err) // a struct of strings always marshals
	}
	line = append(line, '\n')
	// Written asynchronously so that a JVM which stops reading stdin
	// cannot hold the caller past its deadline; the reader fails ch when
	// the process is reaped.
	go func() {
		p.writeMu.Lock()
		defer p.writeMu.Unlock()
		if _, err := p.stdin.Write(line); err != nil {
			p.kill(fmt.Sprintf("request write failed: %v", err))
		}
	}()
	return ch, true
}

// kill sends SIGKILL to the JVM's process group unless it was reaped.
func (p *process) kill(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reason == "" {
		p.reason = reason
	}
	p.dead = true
	if !p.reaped {
		killGroup(p.cmd.Process)
	}
}

func (p *process) cause() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reason != "" {
		return p.reason
	}
	if p.exitErr != nil {
		return p.exitErr.Error()
	}
	return "exit status 0"
}

// read dispatches reply lines until stdout closes, then reaps the JVM and
// fails whatever is still pending.
func (p *process) read(stdout io.Reader, maxBytes int) {
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := readLine(br, maxBytes)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				p.kill(fmt.Sprintf("invalid reply: %v", err))
				_, _ = io.Copy(io.Discard, br)
			}
			break
		}
		var r replyLine
		if err := json.Unmarshal(line, &r); err != nil || r.ID == 0 || (r.XML == nil) == (r.Error == nil) {
			p.kill(fmt.Sprintf("invalid reply line %q", truncate(line, 200)))
			_, _ = io.Copy(io.Discard, br)
			break
		}
		p.mu.Lock()
		ch, ok := p.pending[r.ID]
		delete(p.pending, r.ID)
		p.mu.Unlock()
		if !ok {
			p.log(fmt.Sprintf("checkstyle worker %d: ignoring reply for unknown request %d", p.gen, r.ID))
			continue
		}
		if r.XML != nil {
			ch <- result{xml: *r.XML}
		} else {
			ch <- result{errMsg: r.Error}
		}
	}

	waitErr := p.cmd.Wait()
	p.mu.Lock()
	p.dead = true
	p.reaped = true
	p.exitErr = waitErr
	if waitErr == nil {
		p.exitErr = errors.New("exit status 0")
	}
	pending := p.pending
	p.pending = nil
	p.mu.Unlock()
	for _, ch := range pending {
		ch <- result{err: ErrRestarted}
	}
	close(p.done)
}

// readLine returns one '\n'-terminated line without the terminator.
func readLine(br *bufio.Reader, maxBytes int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > maxBytes+1 {
			return nil, fmt.Errorf("reply line longer than %d bytes", maxBytes)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, errors.New("truncated reply line at end of output")
		default:
			return nil, err
		}
	}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// lineLogger forwards the child's stderr to Log one line at a time.
type lineLogger struct {
	prefix string
	log    func(string)
	buf    []byte
}

func (l *lineLogger) Write(b []byte) (int, error) {
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.log(l.prefix + truncate(l.buf[:i], 4096))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 4096 {
		l.log(l.prefix + truncate(l.buf, 4096))
		l.buf = l.buf[:0]
	}
	return len(b), nil
}
