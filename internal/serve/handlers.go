package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// reqInfo collects what the request log line reports.
type reqInfo struct {
	status   int
	code     string
	language string
	queueMs  int64
	runMs    int64
	// abandoned requests got no response because the client left.
	abandoned bool
}

type infoKey struct{}

func info(r *http.Request) *reqInfo {
	if ri, ok := r.Context().Value(infoKey{}).(*reqInfo); ok {
		return ri
	}
	return &reqInfo{}
}

// statusWriter records the status of a response.
type statusWriter struct {
	http.ResponseWriter
	ri *reqInfo
}

func (w *statusWriter) WriteHeader(status int) {
	if w.ri.status == 0 {
		w.ri.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// handler routes one listener's requests. On TCP every request except the
// two health GETs must carry the token, checked before routing so that an
// unauthenticated client learns nothing about paths.
func (s *Server) handler(tcp bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ri := &reqInfo{}
		r = r.WithContext(context.WithValue(r.Context(), infoKey{}, ri))
		sw := &statusWriter{ResponseWriter: w, ri: ri}
		defer s.logRequest(r, ri, start)

		health := r.Method == http.MethodGet && (r.URL.Path == "/healthz/live" || r.URL.Path == "/healthz/ready")
		if tcp && !health && !s.tokenOK(r) {
			s.writeError(sw, r, http.StatusUnauthorized, "unauthorized", "The request lacks a valid linter token.", "")
			return
		}
		s.route(sw, r, start)
	})
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, start time.Time) {
	p := r.URL.Path
	switch {
	case p == "/v1/manifest":
		if s.allow(w, r, http.MethodGet) {
			s.writeJSON(w, r, http.StatusOK, s.manifest)
		}
	case p == "/v1/lint":
		if s.allow(w, r, http.MethodPost) {
			s.handleLint(w, r, start)
		}
	case strings.HasPrefix(p, "/v1/assets/"):
		if s.allow(w, r, http.MethodPut) {
			s.handleAssets(w, r)
		}
	case p == "/healthz/live":
		if s.allow(w, r, http.MethodGet) {
			s.handleLive(w, r)
		}
	case p == "/healthz/ready":
		if s.allow(w, r, http.MethodGet) {
			s.handleReady(w, r)
		}
	default:
		s.writeError(w, r, http.StatusNotFound, "not_found", "No such endpoint.", "")
	}
}

func (s *Server) allow(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	s.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed",
		fmt.Sprintf("Use %s for this endpoint.", method), "")
	return false
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if held := s.pool.longestHold(); held > 3*s.cfg.Deadline {
		s.writeBody(w, r, http.StatusServiceUnavailable, map[string]string{
			"status": "stuck",
			"reason": fmt.Sprintf("a worker slot has been held for %d ms", held.Milliseconds()),
		})
		return
	}
	s.writeBody(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	status, pending, reason := s.state.snapshot()
	body := struct {
		Status  string   `json:"status"`
		Pending []string `json:"pending,omitempty"`
		Reason  string   `json:"reason,omitempty"`
	}{status, pending, reason}
	code := http.StatusOK
	if status != "ready" {
		code = http.StatusServiceUnavailable
	}
	s.writeBody(w, r, code, body)
}

func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/assets/")
	key, fp, ok := strings.Cut(rest, "/")
	if !ok || !cacheKeyRE.MatchString(key) || !fingerprintRE.MatchString(fp) {
		s.writeError(w, r, http.StatusBadRequest, "bad_request",
			"The asset path must be /v1/assets/{cache_key}/{fingerprint}.", "")
		return
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		w.Header().Set("Retry-After", "1")
		s.writeError(w, r, http.StatusServiceUnavailable, "busy", "Too many asset uploads are in progress.", "")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(uploadTimeout))
	err := s.cache.put(key, fp, http.MaxBytesReader(w, r.Body, s.cfg.CacheMaxBytes))
	_ = rc.SetReadDeadline(time.Time{})
	// A body read that hit the deadline also cancels the request context,
	// so the timeout is answered before the client-gone check.
	if r.Context().Err() != nil && !errors.Is(err, errUploadSlow) {
		info(r).abandoned = true
		return
	}
	var invalid *invalidAssetsError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.As(err, &invalid):
		s.writeError(w, r, http.StatusUnprocessableEntity, "assets_invalid", "The asset archive is not acceptable.", invalid.reason)
	case errors.Is(err, errTooLarge):
		s.writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large",
			fmt.Sprintf("The asset set exceeds %d bytes.", s.cfg.CacheMaxBytes), "")
	case errors.Is(err, errUploadSlow):
		s.writeError(w, r, http.StatusGatewayTimeout, "timeout", "The upload did not finish within 60 seconds.", "")
	case errors.Is(err, errCacheFull):
		s.writeError(w, r, http.StatusInsufficientStorage, "cache_full", "The asset cache cannot make room.", "")
	case errors.Is(err, errNotTar):
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "The body is not a tar stream.", err.Error())
	default:
		s.writeError(w, r, http.StatusInternalServerError, "internal", "Storing the assets failed.", err.Error())
	}
}

// writeError writes the error body of contract section 8.1.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message, details string) {
	info(r).code = code
	if status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "1")
	}
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details string `json:"details,omitempty"`
	}
	s.writeBody(w, r, status, struct {
		Error errBody `json:"error"`
	}{errBody{code, message, details}})
}

func (s *Server) writeBody(w http.ResponseWriter, r *http.Request, status int, v any) {
	b, err := marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b = []byte(`{"error":{"code":"internal","message":"Encoding the response failed."}}` + "\n")
	}
	s.writeJSON(w, r, status, b)
}

// writeJSON sends an encoded body after redacting the token.
func (s *Server) writeJSON(w http.ResponseWriter, _ *http.Request, status int, b []byte) {
	b = s.redact(b)
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

var traceparentRE = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)

func (s *Server) logRequest(r *http.Request, ri *reqInfo, start time.Time) {
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", ri.status),
		slog.Int64("ms", time.Since(start).Milliseconds()),
	}
	if ri.abandoned {
		attrs = append(attrs, slog.Bool("abandoned", true))
	}
	if ri.code != "" {
		attrs = append(attrs, slog.String("code", ri.code))
	}
	if ri.language != "" {
		attrs = append(attrs, slog.String("language", ri.language),
			slog.Int64("queue_ms", ri.queueMs), slog.Int64("run_ms", ri.runMs))
	}
	if m := traceparentRE.FindStringSubmatch(r.Header.Get("traceparent")); m != nil && m[1] != strings.Repeat("0", 32) {
		attrs = append(attrs, slog.String("trace_id", m[1]))
	}
	s.log.LogAttrs(context.Background(), slog.LevelInfo, "request", attrs...)
}
