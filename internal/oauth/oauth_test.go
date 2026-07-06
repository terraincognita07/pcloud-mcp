package oauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/terraincognita07/pcloud-mcp/internal/config"
	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

func TestListenAddrIsLoopback(t *testing.T) {
	got := listenAddr(53682)
	if got != "127.0.0.1:53682" {
		t.Errorf("listenAddr = %q; want loopback", got)
	}
	if strings.HasPrefix(got, "0.0.0.0") {
		t.Error("listener must not bind all interfaces")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	raw := buildAuthorizeURL("my-client", "http://127.0.0.1:53682/callback", "st4te")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "my.pcloud.com" {
		t.Errorf("host = %q", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != "my-client" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("state") != "st4te" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:53682/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestRandomStateUniqueAndLong(t *testing.T) {
	a, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := randomState()
	if a == b {
		t.Error("state values should differ")
	}
	if len(a) < 32 {
		t.Errorf("state too short: %d chars", len(a))
	}
}

func TestApiHostForLocation(t *testing.T) {
	cases := []struct {
		loc      int
		hostname string
		want     string
	}{
		{1, "", "api.pcloud.com"},
		{2, "", "eapi.pcloud.com"},
		{0, "", "api.pcloud.com"},                  // unknown → US
		{99, "", "api.pcloud.com"},                 // garbage → US
		{1, "eapi.pcloud.com", "eapi.pcloud.com"},  // explicit hostname wins
		{2, "evil.example.com", "eapi.pcloud.com"}, // unrecognised hostname ignored
	}
	for _, c := range cases {
		if got := apiHostForLocation(c.loc, c.hostname); got != c.want {
			t.Errorf("apiHostForLocation(%d,%q) = %q; want %q", c.loc, c.hostname, got, c.want)
		}
	}
}

func TestCallbackHandler_Success(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("good-state", ch)
	req := httptest.NewRequest("GET", "/callback?state=good-state&code=abc123&locationid=2&hostname=eapi.pcloud.com", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	res := <-ch
	if res.err != nil {
		t.Fatalf("unexpected err: %v", res.err)
	}
	if res.code != "abc123" || res.locationID != 2 || res.hostname != "eapi.pcloud.com" {
		t.Errorf("result = %+v", res)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "abc123") {
		t.Error("callback page must not echo the code/token")
	}
}

// TestCallbackHandler_StateMismatchDoesNotAbort is the regression test for the
// local-DoS finding: a bogus callback (wrong state) must be rejected over HTTP
// but must NOT push to the channel, so the flow keeps waiting for the real one.
func TestCallbackHandler_StateMismatchDoesNotAbort(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("good-state", ch)
	req := httptest.NewRequest("GET", "/callback?state=ATTACKER&code=abc", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d; want 400", rec.Code)
	}
	select {
	case res := <-ch:
		t.Errorf("a bogus callback must not abort the flow, but it sent %+v", res)
	default:
		// Correct: nothing delivered, Run keeps waiting.
	}
}

// TestCallbackHandler_BogusThenRealSucceeds proves a local attacker racing the
// browser cannot prevent a successful setup: the real callback still completes.
func TestCallbackHandler_BogusThenRealSucceeds(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("good", ch)
	// Attacker races first with the wrong state.
	h(httptest.NewRecorder(), httptest.NewRequest("GET", "/callback?state=bad&code=x", nil))
	// The genuine callback follows.
	h(httptest.NewRecorder(), httptest.NewRequest("GET", "/callback?state=good&code=real&locationid=1", nil))

	res := <-ch
	if res.err != nil || res.code != "real" {
		t.Fatalf("real callback after a bogus one should succeed, got %+v", res)
	}
}

// TestCallbackHandler_ProviderError surfaces an OAuth error redirect (e.g. the
// user denied access) when it carries our state.
func TestCallbackHandler_ProviderError(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("s", ch)
	req := httptest.NewRequest("GET", "/callback?state=s&error=access_denied", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	res := <-ch
	if res.err == nil {
		t.Fatal("expected an error result for a provider error redirect")
	}
	if rec.Code != 400 {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestCallbackHandler_BadLocationIDDoesNotCrash(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("s", ch)
	req := httptest.NewRequest("GET", "/callback?state=s&code=c&locationid=not-a-number", nil)
	rec := httptest.NewRecorder()
	h(rec, req) // must not panic

	res := <-ch
	if res.err != nil {
		t.Fatalf("bad locationid should not error: %v", res.err)
	}
	if res.locationID != 1 {
		t.Errorf("bad locationid should default to 1, got %d", res.locationID)
	}
	if res.code != "c" {
		t.Errorf("code = %q", res.code)
	}
}

// TestRun_CancelledContext proves Run returns promptly and wraps ctx.Err()
// when the caller's context is already cancelled. openBrowser is stubbed so
// the test never opens a real browser window.
func TestRun_CancelledContext(t *testing.T) {
	old := openBrowser
	openBrowser = func(string) error { return nil }
	t.Cleanup(func() { openBrowser = old })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, Config{ClientID: "id", ClientSecret: "secret", Port: 0})
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want wrapped context.Canceled", err)
	}
}

// TestRun_ListenFailure proves Run returns promptly with a wrapped error when
// the loopback callback port is already in use.
func TestRun_ListenFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy a loopback port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	old := openBrowser
	openBrowser = func(string) error {
		t.Error("must not attempt to open a browser when listen fails")
		return nil
	}
	t.Cleanup(func() { openBrowser = old })

	_, err = Run(context.Background(), Config{ClientID: "id", ClientSecret: "secret", Port: port})
	if err == nil {
		t.Fatal("expected an error when the callback port is already in use")
	}
}

// freeLoopbackPort picks a currently free loopback port and releases it for
// Run to bind. The tiny reuse race is acceptable in a test.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick a loopback port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to release the port: %v", err)
	}
	return port
}

// TestRun_SuccessPath drives the whole flow end to end over the real loopback
// listener: Run binds the callback server, the test plays the provider by
// requesting the redirect URI with the genuine state, and a stubbed
// exchangeCode returns the token. Asserts the credentials Run assembles and
// that the EU callback maps to the EU API host.
func TestRun_SuccessPath(t *testing.T) {
	port := freeLoopbackPort(t)

	authURLCh := make(chan string, 1)
	oldBrowser := openBrowser
	openBrowser = func(u string) error {
		authURLCh <- u
		return nil
	}
	t.Cleanup(func() { openBrowser = oldBrowser })

	oldExchange := exchangeCode
	exchangeCode = func(_ context.Context, _ *http.Client, host, clientID, clientSecret, code string) (*pcloud.OAuthToken, error) {
		if host != "eapi.pcloud.com" {
			t.Errorf("exchange host = %q; want eapi.pcloud.com", host)
		}
		if clientID != "id" || clientSecret != "secret" {
			t.Errorf("exchange credentials = %q/%q", clientID, clientSecret)
		}
		if code != "c0de" {
			t.Errorf("exchange code = %q; want c0de", code)
		}
		return &pcloud.OAuthToken{AccessToken: "tok-123", UID: 42}, nil
	}
	t.Cleanup(func() { exchangeCode = oldExchange })

	type runResult struct {
		creds *config.Credentials
		err   error
	}
	done := make(chan runResult, 1)
	go func() {
		creds, err := Run(context.Background(), Config{
			ClientID: "id", ClientSecret: "secret", Port: port, Timeout: 10 * time.Second,
		})
		done <- runResult{creds, err}
	}()

	// openBrowser fires only after the listener is bound, so receiving the
	// auth URL means the callback endpoint is ready.
	authURL := <-authURLCh
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := u.Query().Get("state")
	redirect := u.Query().Get("redirect_uri")

	// Play the provider: deliver the real callback.
	cb := redirect + "?" + url.Values{
		"state": {state}, "code": {"c0de"}, "locationid": {"2"}, "hostname": {"eapi.pcloud.com"},
	}.Encode()
	resp, err := http.Get(cb)
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status = %d; want 200", resp.StatusCode)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("Run: %v", res.err)
	}
	if res.creds.AccessToken != "tok-123" {
		t.Errorf("AccessToken = %q; want tok-123", res.creds.AccessToken)
	}
	if res.creds.Region != 2 {
		t.Errorf("Region = %d; want 2", res.creds.Region)
	}
	if res.creds.UID != 42 {
		t.Errorf("UID = %d; want 42", res.creds.UID)
	}
}

// TestRun_Timeout proves the timeout branch returns errTimedOut when no
// callback ever arrives.
func TestRun_Timeout(t *testing.T) {
	port := freeLoopbackPort(t)

	old := openBrowser
	openBrowser = func(string) error { return nil }
	t.Cleanup(func() { openBrowser = old })

	_, err := Run(context.Background(), Config{
		ClientID: "id", ClientSecret: "secret", Port: port, Timeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, errTimedOut) {
		t.Errorf("err = %v; want errTimedOut", err)
	}
}

func TestCallbackHandler_MissingCode(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("s", ch)
	req := httptest.NewRequest("GET", "/callback?state=s", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	res := <-ch
	if res.err == nil {
		t.Error("expected error for missing code")
	}
}
