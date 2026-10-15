// Package serve implements `cobe-lint serve`, the linter container's main
// process: an HTTP/1.1 JSON API on a unix socket and an optional
// token-checked TCP listener that lints with warm tools (docs/CONTRACT.md
// sections 7 to 9).
package serve

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zinc-sig/linter/internal/checkstyleworker"
	"github.com/zinc-sig/linter/internal/manifest"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

// Checker is the resident checkstyle worker as the server uses it;
// *checkstyleworker.Worker implements it.
type Checker interface {
	Check(ctx context.Context, files []string) (checkstyleworker.Output, error)
	Ready() <-chan struct{}
	Failed() <-chan struct{}
	Err() error
	Close() error
}

// Deps are the parts of the server that tests replace. Zero fields take
// the image defaults (see DefaultDeps).
type Deps struct {
	// Linters are the manifest languages.
	Linters []linter.Linter
	// ManifestVersion is the manifest-schema version served.
	ManifestVersion int
	// ToolOf returns a language's driver id (languages.Tool*).
	ToolOf func(language string) string
	// StartChecker starts the resident checkstyle worker with a pool of
	// threads and a per-request run budget; called only when a language
	// uses checkstyle.
	StartChecker func(threads int, runBudget time.Duration, log func(string)) (Checker, error)
	// GoWarmup fills the Go build cache once; called only when a language
	// uses go vet.
	GoWarmup func(ctx context.Context, s *Server) error
	// Log receives the JSON log lines.
	Log io.Writer
}

// DefaultDeps are the image's languages, checkstyle worker and Go
// warm-up, logging to stderr.
func DefaultDeps(stderr io.Writer) Deps {
	return Deps{
		Linters:         languages.All(),
		ManifestVersion: languages.ManifestVersion(),
		ToolOf:          languages.Tool,
		StartChecker: func(threads int, runBudget time.Duration, log func(string)) (Checker, error) {
			return checkstyleworker.Start(checkstyleworker.Config{Threads: threads, RunBudget: runBudget, Log: log})
		},
		GoWarmup: goWarmup,
		Log:      stderr,
	}
}

// Server is one `cobe-lint serve` process.
type Server struct {
	cfg     Config
	deps    Deps
	log     *slog.Logger
	token   []byte
	redacts [][]byte

	linters  map[string]linter.Linter
	manifest []byte

	pool    *pool
	uploads chan struct{}
	cache   *assetCache
	state   *readiness

	checkerMu sync.Mutex
	checker   Checker

	unixLn net.Listener
	tcpLn  net.Listener

	baseCtx    context.Context
	cancelBase context.CancelFunc
}

// New reads the token, prepares the cache and work directories and binds
// the listeners (contract section 7.3, step 1).
func New(cfg Config, deps Deps) (*Server, error) {
	if deps.Log == nil {
		deps.Log = os.Stderr
	}
	s := &Server{
		cfg:     cfg,
		deps:    deps,
		log:     slog.New(slog.NewJSONHandler(deps.Log, nil)),
		linters: make(map[string]linter.Linter, len(deps.Linters)),
		pool:    newPool(cfg.Workers, cfg.Queue),
		uploads: make(chan struct{}, cfg.Workers),
		cache:   newAssetCache(cfg.CacheDir, cfg.CacheMaxBytes),
	}
	s.baseCtx, s.cancelBase = context.WithCancel(context.Background())

	components := []string{componentWorkers}
	for _, l := range deps.Linters {
		s.linters[l.Language()] = l
	}
	if s.uses(languages.ToolCheckstyle) {
		components = append(components, componentCheckstyle)
	}
	if s.uses(languages.ToolGoVet) {
		components = append(components, componentGoWarmup)
	}
	s.state = newReadiness(components...)

	if err := s.buildManifest(); err != nil {
		return nil, err
	}
	if cfg.TokenFile != "" {
		if err := s.readToken(cfg.TokenFile); err != nil {
			return nil, err
		}
	}
	if err := prepareDir(cfg.CacheDir, func(string) bool { return true }); err != nil {
		return nil, fmt.Errorf("cache directory: %w", err)
	}
	if err := prepareDir(cfg.WorkDir, func(name string) bool { return strings.HasPrefix(name, workspacePrefix) }); err != nil {
		return nil, fmt.Errorf("work directory: %w", err)
	}
	if err := s.bind(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) uses(tool string) bool {
	for key := range s.linters {
		if s.deps.ToolOf(key) == tool {
			return true
		}
	}
	return false
}

func (s *Server) readToken(file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("token file: %w", err)
	}
	token := bytes.TrimRight(b, " \t\r\n\v\f")
	if len(token) < minTokenBytes {
		return fmt.Errorf("token file: the token is shorter than %d bytes", minTokenBytes)
	}
	s.token = token
	s.redacts = [][]byte{token}
	// The JSON-escaped form, in case the token holds characters JSON
	// escapes.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(string(token))
	if esc := bytes.TrimSuffix(bytes.TrimSpace(buf.Bytes()), []byte(`"`)); len(esc) > 1 {
		if esc = esc[1:]; !bytes.Equal(esc, token) {
			s.redacts = append(s.redacts, esc)
		}
	}
	return nil
}

// prepareDir creates dir, removes the children stale reports true for,
// and checks that dir is writable.
func prepareDir(dir string, stale func(name string) bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if stale(e.Name()) {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	probe, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		return err
	}
	probe.Close()
	return os.Remove(probe.Name())
}

func (s *Server) bind() error {
	if fi, err := os.Lstat(s.cfg.Socket); err == nil && !fi.IsDir() {
		_ = os.Remove(s.cfg.Socket)
	}
	ln, err := net.Listen("unix", s.cfg.Socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Socket, err)
	}
	if err := os.Chmod(s.cfg.Socket, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("chmod %s: %w", s.cfg.Socket, err)
	}
	s.unixLn = ln
	if s.cfg.Listen != "" {
		tcp, err := net.Listen("tcp", s.cfg.Listen)
		if err != nil {
			ln.Close()
			return fmt.Errorf("listen on %s: %w", s.cfg.Listen, err)
		}
		s.tcpLn = tcp
	}
	return nil
}

// TCPAddr is the bound TCP address, or nil without --listen.
func (s *Server) TCPAddr() net.Addr {
	if s.tcpLn == nil {
		return nil
	}
	return s.tcpLn.Addr()
}

// Run serves until ctx ends, warming up in the background, then shuts
// down (contract section 7.3).
func (s *Server) Run(ctx context.Context) error {
	servers := []*http.Server{s.httpServer(false)}
	listeners := []net.Listener{s.unixLn}
	if s.tcpLn != nil {
		servers = append(servers, s.httpServer(true))
		listeners = append(listeners, s.tcpLn)
	}
	serveErr := make(chan error, len(servers))
	for i, srv := range servers {
		go func() { serveErr <- srv.Serve(listeners[i]) }()
	}
	s.log.Info("serving", "socket", s.cfg.Socket, "listen", s.cfg.Listen,
		"workers", s.cfg.Workers, "queue", s.cfg.Queue, "deadline_ms", s.cfg.Deadline.Milliseconds())

	s.state.done(componentWorkers)
	var warm sync.WaitGroup
	if s.uses(languages.ToolCheckstyle) {
		if err := s.startChecker(); err != nil {
			s.shutdown(servers)
			return err
		}
	}
	if s.uses(languages.ToolGoVet) {
		warm.Add(1)
		go func() {
			defer warm.Done()
			s.runGoWarmup()
		}()
	}

	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	s.shutdown(servers)
	warm.Wait()
	return err
}

func (s *Server) startChecker() error {
	c, err := s.deps.StartChecker(s.cfg.Workers, s.cfg.Deadline, func(msg string) {
		s.log.Info("checkstyle worker", "event", string(s.redact([]byte(msg))))
	})
	if err != nil {
		return fmt.Errorf("start checkstyle worker: %w", err)
	}
	s.checkerMu.Lock()
	s.checker = c
	s.checkerMu.Unlock()
	go func() {
		select {
		case <-c.Ready():
			s.state.done(componentCheckstyle)
		case <-c.Failed():
		case <-s.baseCtx.Done():
			return
		}
		select {
		case <-c.Failed():
			s.state.fail(fmt.Sprintf("checkstyle worker failed: %v", c.Err()))
		case <-s.baseCtx.Done():
		}
	}()
	return nil
}

func (s *Server) runGoWarmup() {
	start := time.Now()
	ctx, cancel := context.WithTimeout(s.baseCtx, goWarmupTimeout)
	defer cancel()
	if err := s.deps.GoWarmup(ctx, s); err != nil {
		s.log.Warn("go warm-up failed", "error", string(s.redact([]byte(err.Error()))))
	} else {
		s.log.Info("go warm-up done", "ms", time.Since(start).Milliseconds())
	}
	s.state.done(componentGoWarmup)
}

// goWarmupSample imports the standard packages student programs use most,
// so that the first lints find them compiled in GOCACHE.
const goWarmupSample = `package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	v := []float64{math.Sqrt(float64(n))}
	sort.Float64s(v)
	fmt.Println(v)
}
`

// goWarmup lints goWarmupSample once so that GOCACHE is warm (contract
// section 9.5).
func goWarmup(ctx context.Context, s *Server) error {
	for key, l := range s.linters {
		if s.deps.ToolOf(key) != languages.ToolGoVet {
			continue
		}
		ws, err := newWorkspace(s.cfg.WorkDir)
		if err != nil {
			return err
		}
		defer os.RemoveAll(ws)
		if err := writeCode(ws, "warmup.go", goWarmupSample); err != nil {
			return err
		}
		_, err = linter.RunContext(ctx, l, []string{"warmup.go"}, linter.Options{Dir: ws, Env: linter.ScrubbedEnv(l)})
		return err
	}
	return nil
}

func (s *Server) shutdown(servers []*http.Server) {
	s.cancelBase()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if srv.Shutdown(ctx) != nil {
				srv.Close()
			}
		}()
	}
	s.checkerMu.Lock()
	c := s.checker
	s.checkerMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	wg.Wait()
	_ = os.Remove(s.cfg.Socket)
	s.log.Info("stopped")
}

func (s *Server) httpServer(tcp bool) *http.Server {
	return &http.Server{
		Handler:           s.handler(tcp),
		ReadHeaderTimeout: readHeaderWait,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return s.baseCtx },
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
}

// buildManifest renders the GET /v1/manifest body once.
func (s *Server) buildManifest() error {
	m := manifest.Build(s.deps.ManifestVersion, s.deps.Linters)
	doc := struct {
		manifest.Manifest
		Serve serveInfo `json:"serve"`
	}{m, serveInfo{
		Protocol:      Protocol,
		Workers:       s.cfg.Workers,
		Queue:         s.cfg.Queue,
		DeadlineMs:    s.cfg.Deadline.Milliseconds(),
		MaxCodeBytes:  MaxCodeBytes,
		MaxFindings:   MaxFindings,
		CacheMaxBytes: s.cfg.CacheMaxBytes,
	}}
	b, err := marshal(doc)
	if err != nil {
		return err
	}
	s.manifest = b
	return nil
}

type serveInfo struct {
	Protocol      int   `json:"protocol"`
	Workers       int   `json:"workers"`
	Queue         int   `json:"queue"`
	DeadlineMs    int64 `json:"deadline_ms"`
	MaxCodeBytes  int   `json:"max_code_bytes"`
	MaxFindings   int   `json:"max_findings"`
	CacheMaxBytes int64 `json:"cache_max_bytes"`
}

// tokenOK compares the request's token header in constant time.
func (s *Server) tokenOK(r *http.Request) bool {
	got := r.Header.Get(TokenHeader)
	return len(s.token) > 0 && subtle.ConstantTimeCompare([]byte(got), s.token) == 1
}

// redact replaces every occurrence of the token in b.
func (s *Server) redact(b []byte) []byte {
	for _, t := range s.redacts {
		b = bytes.ReplaceAll(b, t, []byte("[redacted]"))
	}
	return b
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isMaxBytes(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
