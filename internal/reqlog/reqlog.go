// Package reqlog gives each request an ID and a logger tagged with it, so
// everything logged while handling a request can be correlated.
package reqlog

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
	"uuid"
)

type loggerKey struct{}

// holder is shared by the whole request, so attributes added down the chain
// (e.g. the user) also end up in the completion line.
type holder struct {
	logger *slog.Logger
}

// Handler takes the request ID from the X-Request-ID header (set by the
// reverse proxy) or generates one (UUIDv7, so IDs sort by time), returns it
// in X-Request-ID, puts a tagged logger into the request context, logs the
// completed request and recovers panics.
func Handler(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validID(id) {
			id = uuid.NewV7().String()
		}
		h := &holder{logger: logger.With("request_id", id, "method", r.Method, "path", r.URL.Path)}
		w.Header().Set("X-Request-ID", id)
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		serve(rw, r.WithContext(context.WithValue(r.Context(), loggerKey{}, h)), next, h)

		level := slog.LevelInfo
		if rw.status >= 500 {
			level = slog.LevelError
		}
		h.logger.Log(r.Context(), level, "request completed",
			"status", rw.status, "duration", time.Since(start))
	})
}

// validID accepts IDs like nginx's $request_id or a UUID; anything else is
// replaced, so a client can't put arbitrary text into the logs.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// serve runs next, logging a panic with the request's logger instead of
// leaving it to net/http, which logs it without the request ID.
func serve(rw *responseWriter, r *http.Request, next http.Handler, h *holder) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if v == http.ErrAbortHandler {
			panic(v)
		}
		h.logger.Error("panic", "panic", v, "stack", string(debug.Stack()))
		if rw.wroteHeader {
			// Too late for an error page, abort the response instead.
			panic(http.ErrAbortHandler)
		}
		http.Error(rw, "Внутренняя ошибка", http.StatusInternalServerError)
	}()
	next.ServeHTTP(rw, r)
}

// From returns the request's logger, or the default one outside a request.
func From(ctx context.Context) *slog.Logger {
	if h, ok := ctx.Value(loggerKey{}).(*holder); ok {
		return h.logger
	}
	return slog.Default()
}

// Add tags the rest of the request's log lines, including the completion
// line, with args. No-op outside a request.
func Add(ctx context.Context, args ...any) {
	if h, ok := ctx.Value(loggerKey{}).(*holder); ok {
		h.logger = h.logger.With(args...)
	}
}

// responseWriter captures the status code for the completion log.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	rw.wroteHeader = true
	return rw.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
