// Package httpserver serves an MCP handler over authenticated HTTP for remote
// access (e.g. from Claude.ai web). It is the network-exposed boundary of the
// project, so its whole job is to let exactly one caller through — the holder of
// a shared bearer token — and refuse everyone else.
//
// Design choices, and why:
//
//   - The bearer token is compared in constant time, so a timing side channel
//     cannot reveal it character by character.
//   - The server refuses to start with an empty token (Serve returns an error).
//     There is no "unauthenticated mode" to fall into by misconfiguration.
//   - ReadHeaderTimeout is set, closing the Slowloris hold-open vector.
//   - Shutdown is graceful on context cancellation.
//
// Authentication here is intentionally a bearer token, not OAuth: this is a
// single-user, self-hosted server. A token in the Authorization header is the
// best-practice fit and adds no login step the MCP client cannot perform.
package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// ReadHeaderTimeout bounds how long a client may take to send request headers.
const ReadHeaderTimeout = 10 * time.Second

// shutdownGrace bounds graceful shutdown after the context is cancelled.
const shutdownGrace = 5 * time.Second

// MinTokenLength is the shortest bearer token Serve accepts. This is a guard
// against a misconfigured, trivially brute-forceable secret (the docs tell
// operators to use `openssl rand -hex 32`, i.e. 64 chars); it is not a strength
// estimator, just a floor so a 1-char token can't be served to the internet.
const MinTokenLength = 16

// BearerAuth wraps next so that only requests carrying "Authorization: Bearer
// <token>" are passed through; everything else gets 401. The comparison is
// constant time. token must be non-empty (callers should validate before use;
// Serve does).
func BearerAuth(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r.Header.Get("Authorization"))
		// Hash both sides to fixed-length digests before the constant-time
		// compare: ConstantTimeCompare alone returns early on a length mismatch,
		// so comparing raw tokens would leak the token's length through timing.
		// Comparing the SHA-256 digests leaks neither content nor length.
		gotSum := sha256.Sum256([]byte(got))
		if got == "" || subtle.ConstantTimeCompare(gotSum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pcloud-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from an Authorization header value, accepting
// the "Bearer " scheme case-insensitively. Returns "" if absent or malformed.
func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// newServer assembles the *http.Server with auth, the Slowloris guard
// (ReadHeaderTimeout), and error logging wired in. It is split out from Serve so
// that wiring is unit-testable without binding a socket. logger must be non-nil.
func newServer(addr, token string, handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           BearerAuth(token, handler),
		ReadHeaderTimeout: ReadHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// Serve runs handler behind bearer auth on addr until ctx is cancelled. It
// fails closed: an empty token is refused rather than served unauthenticated.
func Serve(ctx context.Context, addr, token string, handler http.Handler, logger *slog.Logger) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("httpserver: refusing to start without a bearer token")
	}
	if len(token) < MinTokenLength {
		return fmt.Errorf("httpserver: bearer token too short (%d chars); use at least %d, e.g. `openssl rand -hex 32`", len(token), MinTokenLength)
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(noopWriter{}, nil))
	}

	srv := newServer(addr, token, handler, logger)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen %s: %w", addr, err)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("pcloud-mcp serving over authenticated HTTP", "addr", addr)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("httpserver: %w", err)
	}
}

// noopWriter discards logs when no logger is supplied.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
