package reqlog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"
)

// logLines runs the handler through Handler and returns the response and the
// decoded JSON log lines. requestID is sent as X-Request-ID, if not empty.
func logLines(t *testing.T, requestID string, next http.HandlerFunc) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	Handler(logger, next).ServeHTTP(rec, req)

	var lines []map[string]any
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	return rec, lines
}

func TestHandlerTagsLogs(t *testing.T) {
	rec, lines := logLines(t, "", func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Info("inside")
		w.WriteHeader(http.StatusTeapot)
	})

	id := rec.Header().Get("X-Request-ID")
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("X-Request-ID %q: %v", id, err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %v", len(lines), lines)
	}
	for _, line := range lines {
		if line["request_id"] != id || line["path"] != "/x" || line["method"] != "GET" {
			t.Errorf("line not tagged with the request: %v", line)
		}
	}
	if done := lines[1]; done["msg"] != "request completed" || done["status"] != float64(http.StatusTeapot) {
		t.Errorf("completion line = %v", done)
	}
}

func TestHandlerRecoversPanic(t *testing.T) {
	rec, lines := logLines(t, "", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if len(lines) != 2 || lines[0]["panic"] != "boom" || lines[0]["stack"] == "" {
		t.Fatalf("panic not logged: %v", lines)
	}
	if lines[0]["request_id"] != rec.Header().Get("X-Request-ID") {
		t.Errorf("panic line not tagged with the request ID: %v", lines[0])
	}
	if lines[1]["level"] != "ERROR" || lines[1]["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("completion line = %v", lines[1])
	}
}

func TestFromOutsideRequest(t *testing.T) {
	if From(t.Context()) != slog.Default() {
		t.Error("expected the default logger outside a request")
	}
}

func TestHandlerIncomingID(t *testing.T) {
	cases := map[string]bool{
		"0f8fad5bd9cb469fa16570867728950e":     true, // nginx $request_id
		"01a12b96-f650-7647-a0b9-92a7a2c54c22": true,
		"bad id\nINFO forged":                  false,
		strings.Repeat("a", 65):                false,
	}
	for in, kept := range cases {
		rec, lines := logLines(t, in, func(http.ResponseWriter, *http.Request) {})
		id := rec.Header().Get("X-Request-ID")
		if (id == in) != kept {
			t.Errorf("X-Request-ID %q: got %q, kept = %v", in, id, id == in)
		}
		if lines[0]["request_id"] != id {
			t.Errorf("logged request_id %v, want %q", lines[0]["request_id"], id)
		}
	}
}

func TestAdd(t *testing.T) {
	_, lines := logLines(t, "", func(_ http.ResponseWriter, r *http.Request) {
		Add(r.Context(), "user", "Alice")
		From(r.Context()).Info("inside")
	})
	for _, line := range lines {
		if line["user"] != "Alice" {
			t.Errorf("line not tagged with the user: %v", line)
		}
	}
	Add(t.Context(), "user", "Bob") // outside a request: must not panic
}
