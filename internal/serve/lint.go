package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zinc-sig/linter/internal/checkstyleworker"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

// lintRequest is the POST /v1/lint body; pointers tell a missing field
// from an empty one.
type lintRequest struct {
	Language *string `json:"language"`
	Filename *string `json:"filename"`
	Code     *string `json:"code"`
	Assets   *struct {
		CacheKey    *string `json:"cache_key"`
		Fingerprint *string `json:"fingerprint"`
	} `json:"assets"`
	MaxFindings *int `json:"max_findings"`
}

// lintError is an error response of the lint endpoint.
type lintError struct {
	status  int
	code    string
	message string
	details string
}

func (e *lintError) Error() string { return e.code + ": " + e.message }

func badRequest(format string, args ...any) *lintError {
	return &lintError{status: http.StatusBadRequest, code: "bad_request", message: fmt.Sprintf(format, args...)}
}

// lintJob is a validated lint request.
type lintJob struct {
	linter      linter.Linter
	filename    string
	code        string
	cacheKey    string
	fingerprint string
	maxFindings int
}

func (s *Server) handleLint(w http.ResponseWriter, r *http.Request, start time.Time) {
	ri := info(r)
	ctx, cancel := context.WithDeadline(r.Context(), start.Add(s.cfg.Deadline))
	defer cancel()

	// The read deadline bounds the body only; it is cleared before the lint
	// so that it cannot fire on the idle connection and cancel the request.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(start.Add(s.cfg.Deadline))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLintBody))
	_ = rc.SetReadDeadline(time.Time{})
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		s.writeError(w, r, http.StatusGatewayTimeout, "timeout", "The request body did not arrive within the deadline.", "")
		return
	case err != nil && r.Context().Err() != nil:
		ri.abandoned = true
		return
	case err != nil:
		s.writeLintError(w, r, decodeError(err))
		return
	}
	job, lerr := s.decodeLint(bytes.NewReader(body))
	if lerr != nil {
		s.writeLintError(w, r, lerr)
		return
	}
	ri.language = job.linter.Language()
	if !s.state.isReady() {
		s.writeError(w, r, http.StatusServiceUnavailable, "linter_not_ready", "The linter is still starting.", "")
		return
	}
	if !s.pool.admit() {
		s.writeError(w, r, http.StatusServiceUnavailable, "busy", "All lint workers are busy.", "")
		return
	}
	defer s.pool.leave()

	queueStart := time.Now()
	release, err := s.pool.acquire(ctx)
	ri.queueMs = time.Since(queueStart).Milliseconds()
	if err != nil {
		s.lintFailed(w, r, ctx, err)
		return
	}
	defer release()
	w.Header().Set("X-Zinc-Linter-Queue-Ms", strconv.FormatInt(ri.queueMs, 10))

	runStart := time.Now()
	report, err := s.lint(ctx, job)
	ri.runMs = time.Since(runStart).Milliseconds()
	w.Header().Set("X-Zinc-Linter-Run-Ms", strconv.FormatInt(ri.runMs, 10))
	if err != nil {
		s.lintFailed(w, r, ctx, err)
		return
	}
	if r.Context().Err() != nil {
		ri.abandoned = true // a Java result arriving after the client left
		return
	}

	capReport(&report, job.maxFindings)
	out, err := marshal(report)
	switch {
	case err != nil:
		s.writeError(w, r, http.StatusInternalServerError, "internal", "Encoding the report failed.", err.Error())
	case len(out) > maxResponseBytes:
		s.writeError(w, r, http.StatusInternalServerError, "internal", "The report exceeds the response size limit.", "")
	default:
		s.writeJSON(w, r, http.StatusOK, out)
	}
}

// lintFailed answers a lint that did not produce a report. A request
// whose client left gets no response.
func (s *Server) lintFailed(w http.ResponseWriter, r *http.Request, ctx context.Context, err error) {
	var lerr *lintError
	switch {
	case r.Context().Err() != nil:
		info(r).abandoned = true
		return
	case errors.As(err, &lerr):
	case errors.Is(err, checkstyleworker.ErrTimeout), errors.Is(ctx.Err(), context.DeadlineExceeded):
		lerr = &lintError{http.StatusGatewayTimeout, "timeout", "The lint did not finish within its deadline.", ""}
	case errors.Is(err, checkstyleworker.ErrRestarted):
		lerr = &lintError{http.StatusServiceUnavailable, "linter_not_ready", "The checkstyle worker restarted during the lint.", ""}
	case errors.Is(err, checkstyleworker.ErrFailed), errors.Is(err, checkstyleworker.ErrClosed):
		lerr = &lintError{http.StatusServiceUnavailable, "linter_not_ready", "The checkstyle worker is not available.", ""}
	default:
		lerr = &lintError{http.StatusInternalServerError, "linter_execution_failed", "The linter failed.", err.Error()}
	}
	s.writeLintError(w, r, lerr)
}

func (s *Server) writeLintError(w http.ResponseWriter, r *http.Request, e *lintError) {
	s.writeError(w, r, e.status, e.code, e.message, e.details)
}

// decodeLint parses and validates the body in the order of contract
// section 8.4.
func (s *Server) decodeLint(body io.Reader) (*lintJob, *lintError) {
	dec := json.NewDecoder(body)
	var req lintRequest
	if err := dec.Decode(&req); err != nil {
		return nil, decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil && isMaxBytes(err) {
			return nil, decodeError(err)
		}
		return nil, badRequest("The body must be one JSON object.")
	}

	if req.Language == nil {
		return nil, badRequest("The language field is required.")
	}
	l, ok := s.linters[*req.Language]
	if !ok {
		return nil, &lintError{status: http.StatusUnprocessableEntity, code: "unsupported_language",
			message: fmt.Sprintf("Language %q is not supported.", *req.Language)}
	}
	if req.Filename == nil {
		return nil, badRequest("The filename field is required.")
	}
	if err := checkFilename(*req.Filename); err != nil {
		return nil, &lintError{status: http.StatusUnprocessableEntity, code: "filename_invalid",
			message: "The filename is not a valid workspace path.", details: err.Error()}
	}
	if req.Code == nil {
		return nil, badRequest("The code field is required.")
	}
	if len(*req.Code) > MaxCodeBytes {
		return nil, &lintError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large",
			message: fmt.Sprintf("The code exceeds %d bytes.", MaxCodeBytes)}
	}
	job := &lintJob{linter: l, filename: *req.Filename, code: *req.Code, maxFindings: MaxFindings}
	if a := req.Assets; a != nil {
		if a.CacheKey == nil || !cacheKeyRE.MatchString(*a.CacheKey) {
			return nil, badRequest("assets.cache_key is missing or malformed.")
		}
		if a.Fingerprint == nil || !fingerprintRE.MatchString(*a.Fingerprint) {
			return nil, badRequest("assets.fingerprint is missing or malformed.")
		}
		job.cacheKey, job.fingerprint = *a.CacheKey, *a.Fingerprint
	}
	if req.MaxFindings != nil {
		if *req.MaxFindings < 1 || *req.MaxFindings > MaxFindings {
			return nil, badRequest("max_findings must be between 1 and %d.", MaxFindings)
		}
		job.maxFindings = *req.MaxFindings
	}
	return job, nil
}

func decodeError(err error) *lintError {
	if isMaxBytes(err) {
		return &lintError{status: http.StatusRequestEntityTooLarge, code: "payload_too_large",
			message: fmt.Sprintf("The request body exceeds %d bytes.", maxLintBody)}
	}
	return badRequest("The body is not a valid lint request: %v.", err)
}

// checkFilename applies the filename rules of contract section 8.4.
func checkFilename(name string) error {
	switch {
	case name == "":
		return errors.New("empty filename")
	case len(name) > 255:
		return errors.New("filename longer than 255 bytes")
	case hasControl(name):
		return errors.New("control character in filename")
	case strings.Contains(name, `\`):
		return errors.New("backslash in filename")
	case strings.HasPrefix(name, "/"), strings.HasPrefix(name, "-"):
		return errors.New("filename starts with / or -")
	case name == ".", path.Clean(name) != name:
		return errors.New("filename is not a clean relative path")
	case slices.Contains(strings.Split(name, "/"), ".."):
		return errors.New("filename has a .. segment")
	}
	first, _, _ := strings.Cut(name, "/")
	if first == ".zinc" || first == "examination-assets" {
		return fmt.Errorf("filename is under the reserved directory %s", first)
	}
	return nil
}

// workspacePrefix names the per-request workspaces under --work-dir.
const workspacePrefix = "cobe-lint-ws-"

func newWorkspace(workDir string) (string, error) {
	return os.MkdirTemp(workDir, workspacePrefix)
}

// writeCode writes the student file, replacing an asset of the same path.
func writeCode(ws, filename, code string) error {
	target := filepath.Join(ws, filepath.FromSlash(filename))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(code), 0o644)
}

// lint prepares the workspace and runs the tool; it holds the caller's
// worker slot throughout.
func (s *Server) lint(ctx context.Context, job *lintJob) (linter.Report, error) {
	ws, err := newWorkspace(s.cfg.WorkDir)
	if err != nil {
		return linter.Report{}, setupFailed(err)
	}
	// A one-shot tool has exited by the time this runs. A Java request
	// that passed its deadline may still be on the checkstyle worker, which
	// may then find the file gone; its reply is discarded anyway.
	defer os.RemoveAll(ws)

	if job.fingerprint != "" {
		entry, ok := s.cache.acquire(job.cacheKey, job.fingerprint)
		if !ok {
			return linter.Report{}, &lintError{status: http.StatusConflict, code: "assets_required",
				message: "The asset set is not cached; upload it and retry."}
		}
		err := copyAssets(entry.dir, ws)
		s.cache.release(entry)
		if err != nil {
			return linter.Report{}, setupFailed(err)
		}
	}
	if err := writeCode(ws, job.filename, job.code); err != nil {
		return linter.Report{}, setupFailed(err)
	}
	if err := ctx.Err(); err != nil {
		return linter.Report{}, err
	}

	files := []string{job.filename}
	l := job.linter
	if s.deps.ToolOf(l.Language()) == languages.ToolCheckstyle {
		s.checkerMu.Lock()
		c := s.checker
		s.checkerMu.Unlock()
		if c == nil {
			return linter.Report{}, checkstyleworker.ErrFailed
		}
		out, err := c.Check(ctx, []string{filepath.Join(ws, filepath.FromSlash(job.filename))})
		if err != nil {
			return linter.Report{}, err
		}
		report, err := l.Parse(s.redact(out.Stdout), s.redact(out.Stderr), out.ExitCode)
		if err != nil {
			return linter.Report{}, err
		}
		for i := range report.Findings {
			report.Findings[i].Path = linter.NormalizePath(report.Findings[i].Path, files)
		}
		return report, nil
	}
	return linter.RunContext(ctx, l, files, linter.Options{Dir: ws, Env: linter.ScrubbedEnv(l), Redact: s.redact})
}

func setupFailed(err error) *lintError {
	return &lintError{status: http.StatusInternalServerError, code: "workspace_setup_failed",
		message: "The lint workspace could not be prepared.", details: err.Error()}
}

// capReport applies max_findings and the message length cap.
func capReport(r *linter.Report, maxFindings int) {
	if r.Findings == nil {
		r.Findings = []linter.Finding{}
	}
	if len(r.Findings) > maxFindings {
		r.Findings = r.Findings[:maxFindings]
	}
	for i := range r.Findings {
		r.Findings[i].Message = truncateUTF8(r.Findings[i].Message, maxMessageBytes)
	}
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
