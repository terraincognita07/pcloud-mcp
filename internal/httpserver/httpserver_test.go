package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// okHandler is a stand-in for the wrapped MCP handler; reaching it means auth
// passed.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "reached")
})

func do(t *testing.T, h http.Handler, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBearerAuth_AcceptsCorrectToken(t *testing.T) {
	h := BearerAuth("s3cret", okHandler)
	rec := do(t, h, "Bearer s3cret")
	if rec.Code != http.StatusOK || rec.Body.String() != "reached" {
		t.Errorf("correct token: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestBearerAuth_AcceptsCaseInsensitiveScheme(t *testing.T) {
	h := BearerAuth("s3cret", okHandler)
	if rec := do(t, h, "bearer s3cret"); rec.Code != http.StatusOK {
		t.Errorf("lowercase scheme rejected: %d", rec.Code)
	}
}

// Note: BearerAuth compares with subtle.ConstantTimeCompare. The constant-time
// *timing* property is deliberately not unit-tested — a timing assertion is
// inherently flaky and non-portable; we pin the behavioral contract (reject
// wrong/prefix/missing, accept correct) instead.
func TestBearerAuth_RejectsWrongMissingMalformed(t *testing.T) {
	h := BearerAuth("s3cret", okHandler)
	cases := map[string]string{
		"wrong token":   "Bearer nope",
		"no header":     "",
		"no scheme":     "s3cret",
		"wrong scheme":  "Basic s3cret",
		"empty bearer":  "Bearer ",
		"prefix of tok": "Bearer s3cre",
	}
	for name, hdr := range cases {
		rec := do(t, h, hdr)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: code=%d; want 401", name, rec.Code)
		}
		if rec.Body.String() == "reached" {
			t.Errorf("%s: handler was reached despite bad auth", name)
		}
	}
}

func TestBearerAuth_Sets401Challenge(t *testing.T) {
	h := BearerAuth("s3cret", okHandler)
	rec := do(t, h, "")
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("401 should carry a WWW-Authenticate challenge header")
	}
}

func TestServe_RefusesEmptyToken(t *testing.T) {
	err := Serve(context.Background(), "127.0.0.1:0", "", okHandler, nil)
	if err == nil {
		t.Fatal("Serve must refuse to start without a token")
	}
}

func TestServe_RefusesWhitespaceToken(t *testing.T) {
	if err := Serve(context.Background(), "127.0.0.1:0", "   ", okHandler, nil); err == nil {
		t.Error("Serve must refuse a whitespace-only token")
	}
}

// TestServe_RefusesShortToken guards the MinTokenLength floor: a too-short
// (trivially brute-forceable) secret must not be served to the network.
func TestServe_RefusesShortToken(t *testing.T) {
	short := "abc123"
	if len(short) >= MinTokenLength {
		t.Fatalf("test token unexpectedly long enough: %d", len(short))
	}
	if err := Serve(context.Background(), "127.0.0.1:0", short, okHandler, nil); err == nil {
		t.Error("Serve must refuse a token shorter than MinTokenLength")
	}
}

// validToken meets MinTokenLength for tests that need Serve to actually start.
const validToken = "0123456789abcdef0123" // 20 chars

// TestServe_StartsAndStops binds an ephemeral port, confirms it shuts down
// cleanly when the context is cancelled, and that Serve returns without error.
func TestServe_StartsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "127.0.0.1:0", validToken, okHandler, nil) }()

	// Give it a moment to bind, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned error on clean shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":   "abc",
		"bearer abc":   "abc",
		"BEARER abc":   "abc",
		"Bearer  abc ": "abc",
		"Basic abc":    "",
		"abc":          "",
		"":             "",
		"Bearer":       "",
		"Bearer ":      "",
	}
	for in, want := range cases {
		if got := bearerToken(in); got != want {
			t.Errorf("bearerToken(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestNewServer_WiresReadHeaderTimeout pins the Slowloris guard: the server must
// be built with ReadHeaderTimeout set. Removing the field at the construction
// site would pass every other test, so this asserts the wiring directly.
func TestNewServer_WiresReadHeaderTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer("127.0.0.1:0", validToken, okHandler, logger)
	if srv.ReadHeaderTimeout != ReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v; want %v (Slowloris guard)", srv.ReadHeaderTimeout, ReadHeaderTimeout)
	}
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout is zero — the Slowloris hold-open vector is open")
	}
}
