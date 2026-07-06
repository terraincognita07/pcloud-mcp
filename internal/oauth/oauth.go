// Package oauth implements the pCloud OAuth 2.0 authorization-code flow for a
// local command-line setup, hardened against the weaknesses of the reference
// implementation:
//
//   - The loopback callback server binds to 127.0.0.1, not 0.0.0.0, so no other
//     host on the network can reach it or race the real callback.
//   - A random state parameter is generated and verified on the callback,
//     closing the OAuth CSRF hole (RFC 6749 §10.12).
//   - A malformed locationid in the callback no longer crashes the handler; it
//     falls back to the US region.
//   - The access token is never printed to stdout. On success the caller saves
//     it to a 0600 file; the browser sees only a generic "you may close this
//     window" page.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/terraincognita07/pcloud-mcp/internal/config"
	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

const (
	// DefaultPort is the loopback callback port. The pCloud app's redirect_uri
	// must be registered as http://127.0.0.1:53682/callback.
	DefaultPort = 53682
	// DefaultTimeout bounds how long Run waits for the user to authorize.
	DefaultTimeout = 5 * time.Minute

	authorizeEndpoint = "https://my.pcloud.com/oauth2/authorize"
)

// Config holds the inputs to the OAuth flow.
type Config struct {
	ClientID     string
	ClientSecret string
	Port         int           // 0 → DefaultPort
	Timeout      time.Duration // 0 → DefaultTimeout
}

// launch is the single, audited point where this package shells out. The
// program name is a fixed constant and the only variable argument is a URL this
// package constructed itself (buildAuthorizeURL: our client id, a fixed loopback
// redirect, and a random state). It is passed as a separate argv element, never
// through a shell, so there is no command-injection surface.
func launch(name string, args ...string) error {
	// #nosec G204 -- fixed program name; the only variable arg is a
	// self-constructed URL passed as argv (not via a shell).
	return exec.Command(name, args...).Start()
}

// errTimedOut is returned when the user does not complete authorization within
// the configured timeout.
var errTimedOut = errors.New("oauth: timed out waiting for authorization")

// exchangeCode is a variable so tests can stub the token exchange and cover
// Run's success path without a live pCloud endpoint.
var exchangeCode = pcloud.ExchangeOAuthCode

// openBrowser is a variable so tests can stub it; opening a browser is always
// best-effort, since the URL is also printed for the user to open manually.
var openBrowser = func(u string) error {
	switch runtime.GOOS {
	case "windows":
		return launch("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		return launch("open", u)
	default:
		return launch("xdg-open", u)
	}
}

// Run executes the full flow and returns the credentials to persist. It blocks
// until the user authorizes, the context is cancelled, or the timeout elapses.
func Run(ctx context.Context, cfg Config) (*config.Credentials, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("oauth: client id and client secret are required")
	}
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	state, err := randomState()
	if err != nil {
		return nil, err
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	authURL := buildAuthorizeURL(cfg.ClientID, redirectURI, state)

	// Bind loopback only — the central network-exposure fix.
	ln, err := net.Listen("tcp", listenAddr(port))
	if err != nil {
		return nil, fmt.Errorf("oauth: cannot bind %s: %w", listenAddr(port), err)
	}

	resultCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", callbackHandler(state, resultCh))
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers, closing the Slowloris hold-open vector even on this short-lived
	// loopback listener.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }() // Serve always returns a non-nil error on close
	defer srv.Close()

	fmt.Fprintln(os.Stderr, "Open this URL in your browser to authorize pCloud access:")
	fmt.Fprintln(os.Stderr, "  "+authURL)
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, errTimedOut
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		host := apiHostForLocation(res.locationID, res.hostname)
		tok, err := exchangeCode(ctx, nil, host, cfg.ClientID, cfg.ClientSecret, res.code)
		if err != nil {
			return nil, err
		}
		return &config.Credentials{
			AccessToken: tok.AccessToken,
			Region:      res.locationID,
			UID:         tok.UID,
		}, nil
	}
}

// callbackResult carries the outcome of one callback request.
type callbackResult struct {
	code       string
	locationID int
	hostname   string
	err        error
}

// trySend delivers r without ever blocking. The channel is buffered for one
// result; a second (e.g. retried) callback is dropped rather than leaking a
// blocked handler goroutine.
func trySend(ch chan<- callbackResult, r callbackResult) {
	select {
	case ch <- r:
	default:
	}
}

// callbackHandler verifies state, extracts the code, and parses locationid
// defensively.
//
// Crucially, a request that does NOT carry our state is rejected over HTTP but
// does NOT abort the flow: it is not pushed to the result channel, so the setup
// keeps waiting for the genuine callback. Otherwise any local process could
// race the browser with GET /callback?state=anything and repeatedly kill setup
// (a local denial of service). Only a request carrying the 256-bit state — the
// real provider callback, since guessing it is infeasible — is treated as
// authoritative. State is compared in constant time.
//
// A bad locationid falls back to US (region 1) instead of crashing, the
// concrete fix for the reference server which called int() on the raw value.
func callbackHandler(wantState string, ch chan<- callbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(wantState)) != 1 {
			// Not our callback (or a forgery). Reject, but keep waiting.
			http.Error(w, "unexpected callback", http.StatusBadRequest)
			return
		}

		// From here the request carries our state, so it is the real callback.
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization denied", http.StatusBadRequest)
			trySend(ch, callbackResult{err: fmt.Errorf("oauth: authorization failed: %s", e)})
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			trySend(ch, callbackResult{err: errors.New("oauth: callback missing authorization code")})
			return
		}

		locationID := 1 // default US
		if raw := q.Get("locationid"); raw != "" {
			if n, convErr := strconv.Atoi(raw); convErr == nil {
				locationID = n
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, successHTML)
		trySend(ch, callbackResult{code: code, locationID: locationID, hostname: q.Get("hostname")})
	}
}

// apiHostForLocation chooses the API host, preferring an explicit, recognised
// callback hostname and otherwise mapping from locationid (2 = EU, else US).
func apiHostForLocation(locationID int, hostname string) string {
	switch hostname {
	case "api.pcloud.com", "eapi.pcloud.com":
		return hostname
	}
	if locationID == 2 {
		return "eapi.pcloud.com"
	}
	return "api.pcloud.com"
}

// listenAddr is the loopback bind address. Factored out so a test can assert it
// is never 0.0.0.0.
func listenAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func buildAuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return authorizeEndpoint + "?" + q.Encode()
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

const successHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>pCloud authorized</title></head>
<body style="font-family:sans-serif;max-width:32rem;margin:4rem auto;text-align:center">
<h1>Authorization complete</h1>
<p>You can close this window and return to the terminal.</p>
</body></html>`
