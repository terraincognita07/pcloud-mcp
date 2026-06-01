// Package pcloud is a small, dependency-free client for the pCloud HTTP API.
//
// Design choices that differ from a naive client, and why:
//
//   - The access token is sent as a POST body parameter, never in the URL query
//     string. pCloud accepts access_token either way, but a token in a query
//     string leaks into server access logs, proxy logs, and browser history.
//     Keeping it in the body closes that exposure.
//
//   - Every call takes a context.Context and uses http.NewRequestWithContext, so
//     a slow download or a hung API call can be cancelled by the caller (the MCP
//     runtime cancels the context when a tool call is abandoned).
//
//   - The token is never logged. Client.String redacts it so the struct is safe
//     to print in diagnostics.
//
// The package deliberately knows nothing about the local filesystem. Turning a
// remote listing into local files is the job of the download package, which runs
// every path through internal/safepath. Keeping the client filesystem-free makes
// it testable with httptest and keeps the security boundary in one place.
package pcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Region identifies which pCloud data region an account lives in. The region is
// returned as locationid by the OAuth token exchange and fixes which API host
// every subsequent call must use.
type Region int

const (
	RegionUS Region = 1 // api.pcloud.com
	RegionEU Region = 2 // eapi.pcloud.com
)

// apiHost returns the API host for the region, defaulting to US for any
// unrecognised value so a malformed locationid fails loudly on a real request
// rather than silently producing an empty host.
func (r Region) apiHost() string {
	if r == RegionEU {
		return "eapi.pcloud.com"
	}
	return "api.pcloud.com"
}

// Client talks to one pCloud account in one region.
type Client struct {
	http   *http.Client
	host   string
	token  string
	scheme string // "https" in production; overridable for tests
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (timeouts, transport).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// withBaseURL points the client at an arbitrary host+scheme. It exists for tests
// (httptest gives an http:// URL); production code uses New and stays on https.
func withBaseURL(raw string) Option {
	return func(c *Client) {
		if u, err := url.Parse(raw); err == nil {
			c.scheme = u.Scheme
			c.host = u.Host
		}
	}
}

// New returns a Client for the given OAuth access token and region.
func New(token string, region Region, opts ...Option) *Client {
	c := &Client{
		http:   &http.Client{Timeout: 0}, // no overall timeout: large downloads use ctx
		host:   region.apiHost(),
		token:  token,
		scheme: "https",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// String redacts the token so the client is safe to log.
func (c *Client) String() string {
	return fmt.Sprintf("pcloud.Client{host:%s, token:<redacted>}", c.host)
}

// APIError is a non-zero pCloud result code with its message. pCloud signals
// application errors in the JSON body (HTTP status stays 200), so callers must
// inspect this rather than the HTTP status.
type APIError struct {
	Result  int
	Message string
	Method  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pcloud %s: result %d: %s", e.Method, e.Result, e.Message)
}

// envelope is the common header every pCloud JSON response carries.
type envelope struct {
	Result int    `json:"result"`
	Error  string `json:"error"`
}

// Metadata describes a file or folder as returned by the API. Numeric IDs are
// decoded as int64; only the fields the server actually populates for a given
// kind are non-zero (e.g. FileID/Size for files, FolderID/Contents for folders).
type Metadata struct {
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	IsFolder       bool       `json:"isfolder"`
	FolderID       int64      `json:"folderid"`
	FileID         int64      `json:"fileid"`
	ParentFolderID int64      `json:"parentfolderid"`
	Size           int64      `json:"size"`
	ContentType    string     `json:"contenttype"`
	Hash           int64      `json:"hash"`
	Created        string     `json:"created"`
	Modified       string     `json:"modified"`
	Contents       []Metadata `json:"contents"`
}

// call performs a form-encoded POST to /method with the access token in the
// body, then decodes the JSON response into out (which must embed envelope so
// the result code can be checked). It is the single choke point for auth and
// error handling.
func (c *Client) call(ctx context.Context, method string, params url.Values, out interface{}) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("access_token", c.token)

	endpoint := c.scheme + "://" + c.host + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("pcloud %s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.do(req, method, out)
}

// do sends req and decodes the envelope + payload, mapping a non-zero result to
// an *APIError. Shared by call and uploadFile.
func (c *Client) do(req *http.Request, method string, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pcloud %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("pcloud %s: read body: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pcloud %s: http %d", method, resp.StatusCode)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("pcloud %s: decode envelope: %w", method, err)
	}
	if env.Result != 0 {
		return &APIError{Result: env.Result, Message: env.Error, Method: method}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("pcloud %s: decode payload: %w", method, err)
		}
	}
	return nil
}

// ListFolder returns the metadata for folderID. When recursive is true the whole
// subtree is populated via nested Contents, which is what download uses to walk a
// folder in a single round trip.
func (c *Client) ListFolder(ctx context.Context, folderID int64, recursive bool) (*Metadata, error) {
	params := url.Values{}
	params.Set("folderid", strconv.FormatInt(folderID, 10))
	if recursive {
		params.Set("recursive", "1")
	}
	var out struct {
		envelope
		Metadata Metadata `json:"metadata"`
	}
	if err := c.call(ctx, "listfolder", params, &out); err != nil {
		return nil, err
	}
	return &out.Metadata, nil
}

// GetFileLink resolves a direct, time-limited download URL for fileID. forceDl
// asks pCloud to serve the file as an attachment (application/octet-stream),
// which avoids the CDN second-guessing the content type.
func (c *Client) GetFileLink(ctx context.Context, fileID int64, forceDl bool) (string, error) {
	params := url.Values{}
	params.Set("fileid", strconv.FormatInt(fileID, 10))
	if forceDl {
		params.Set("forcedownload", "1")
	}
	var out struct {
		envelope
		Hosts   []string `json:"hosts"`
		Path    string   `json:"path"`
		Expires string   `json:"expires"`
	}
	if err := c.call(ctx, "getfilelink", params, &out); err != nil {
		return "", err
	}
	if len(out.Hosts) == 0 || out.Path == "" {
		return "", fmt.Errorf("pcloud getfilelink: empty hosts or path in response")
	}
	return buildDownloadURL(out.Hosts[0], out.Path)
}

// buildDownloadURL safely assembles a CDN download URL from the host and
// pre-encoded path returned by getfilelink. The host and path come from the API
// response, so a compromised or MITM'd upstream could try to redirect the
// download to an attacker host via URL confusion — e.g. a path beginning with
// "@evil.com/" turns "https://"+host+path into "https://host@evil.com/",
// reparsing evil.com as the host and exfiltrating the requested file.
//
// Two structural checks close this without constraining legitimate CDN names:
// the host must be a bare hostname (no separators, userinfo, or query), and the
// path must begin with "/" so it cannot smuggle userinfo or a new authority.
// As a final gate the assembled URL is reparsed and its host must equal the
// input host with no userinfo present.
func buildDownloadURL(host, path string) (string, error) {
	if strings.ContainsAny(host, "/@?#: \t") {
		return "", fmt.Errorf("pcloud getfilelink: suspicious host %q", host)
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("pcloud getfilelink: path does not start with '/': %q", path)
	}
	raw := "https://" + host + path
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("pcloud getfilelink: unparseable download URL: %w", err)
	}
	if u.Host != host || u.User != nil {
		return "", fmt.Errorf("pcloud getfilelink: download URL host mismatch (got %q)", u.Host)
	}
	return raw, nil
}

// Download streams the bytes of a direct file link into w. It is a thin GET on
// the CDN host; the link must come from GetFileLink. The body is copied with the
// caller's context so a cancelled tool call aborts a large transfer promptly.
func (c *Client) Download(ctx context.Context, fileURL string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return 0, fmt.Errorf("pcloud download: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("pcloud download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("pcloud download: http %d", resp.StatusCode)
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("pcloud download: copy: %w", err)
	}
	return n, nil
}

// UploadFile uploads content as filename into folderID. The request is
// multipart/form-data with parameters before the file part, as pCloud requires.
// The supplied filename is sent verbatim as the form field; the caller is
// responsible for it being a sane single name (the MCP layer validates it).
func (c *Client) UploadFile(ctx context.Context, folderID int64, filename string, content io.Reader) (*Metadata, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Parameters first, then the file part — pCloud parses in order.
	for k, v := range map[string]string{
		"access_token": c.token,
		"folderid":     strconv.FormatInt(folderID, 10),
		"nopartial":    "1", // never persist a truncated upload
	} {
		if err := mw.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("pcloud uploadfile: write field %s: %w", k, err)
		}
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("pcloud uploadfile: create file part: %w", err)
	}
	if _, err := io.Copy(part, content); err != nil {
		return nil, fmt.Errorf("pcloud uploadfile: copy content: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("pcloud uploadfile: close writer: %w", err)
	}

	endpoint := c.scheme + "://" + c.host + "/uploadfile"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("pcloud uploadfile: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	var out struct {
		envelope
		Metadata []Metadata `json:"metadata"`
	}
	if err := c.do(req, "uploadfile", &out); err != nil {
		return nil, err
	}
	if len(out.Metadata) == 0 {
		return nil, fmt.Errorf("pcloud uploadfile: no metadata returned")
	}
	return &out.Metadata[0], nil
}

// CreateFolder creates name inside parentID and returns the new folder. pCloud's
// createfolder is idempotent-ish; use CreateFolderIfNotExists semantics by
// catching the "already exists" result at the call site if needed.
func (c *Client) CreateFolder(ctx context.Context, parentID int64, name string) (*Metadata, error) {
	params := url.Values{}
	params.Set("folderid", strconv.FormatInt(parentID, 10))
	params.Set("name", name)
	var out struct {
		envelope
		Metadata Metadata `json:"metadata"`
	}
	if err := c.call(ctx, "createfolder", params, &out); err != nil {
		return nil, err
	}
	return &out.Metadata, nil
}

// DeleteFile permanently deletes fileID.
func (c *Client) DeleteFile(ctx context.Context, fileID int64) error {
	params := url.Values{}
	params.Set("fileid", strconv.FormatInt(fileID, 10))
	return c.call(ctx, "deletefile", params, nil)
}

// DeleteFolderRecursive deletes folderID and everything under it. This is
// irreversible; the MCP layer must gate it behind explicit user intent.
func (c *Client) DeleteFolderRecursive(ctx context.Context, folderID int64) error {
	params := url.Values{}
	params.Set("folderid", strconv.FormatInt(folderID, 10))
	return c.call(ctx, "deletefolderrecursive", params, nil)
}

// RenameFile renames and/or moves fileID. Pass toFolderID = 0 to keep the file
// in place and only change its name; pass a non-zero toFolderID to move it.
func (c *Client) RenameFile(ctx context.Context, fileID, toFolderID int64, newName string) (*Metadata, error) {
	params := url.Values{}
	params.Set("fileid", strconv.FormatInt(fileID, 10))
	if toFolderID != 0 {
		params.Set("tofolderid", strconv.FormatInt(toFolderID, 10))
	}
	if newName != "" {
		params.Set("toname", newName)
	}
	var out struct {
		envelope
		Metadata Metadata `json:"metadata"`
	}
	if err := c.call(ctx, "renamefile", params, &out); err != nil {
		return nil, err
	}
	return &out.Metadata, nil
}

// RenameFolder renames and/or moves folderID, mirroring RenameFile.
func (c *Client) RenameFolder(ctx context.Context, folderID, toFolderID int64, newName string) (*Metadata, error) {
	params := url.Values{}
	params.Set("folderid", strconv.FormatInt(folderID, 10))
	if toFolderID != 0 {
		params.Set("tofolderid", strconv.FormatInt(toFolderID, 10))
	}
	if newName != "" {
		params.Set("toname", newName)
	}
	var out struct {
		envelope
		Metadata Metadata `json:"metadata"`
	}
	if err := c.call(ctx, "renamefolder", params, &out); err != nil {
		return nil, err
	}
	return &out.Metadata, nil
}

// GetFilePubLink creates a public share link for fileID and returns the link
// URL. Sharing is an outward-facing action; the MCP layer should confirm intent.
func (c *Client) GetFilePubLink(ctx context.Context, fileID int64) (string, error) {
	params := url.Values{}
	params.Set("fileid", strconv.FormatInt(fileID, 10))
	var out struct {
		envelope
		Link string `json:"link"`
	}
	if err := c.call(ctx, "getfilepublink", params, &out); err != nil {
		return "", err
	}
	return out.Link, nil
}

// CreateUploadLink creates a public, anonymous upload link that lets anyone
// with the URL upload files into folderID. comment is shown to the uploader and
// is required by pCloud. This is an outward-facing capability — it opens a
// write path into the account that needs no authentication — so the MCP layer
// tags the tool accordingly and the caller should confirm intent.
//
// pCloud returns a ready-to-share https URL in the link field; it is returned
// verbatim.
func (c *Client) CreateUploadLink(ctx context.Context, folderID int64, comment string) (string, error) {
	params := url.Values{}
	params.Set("folderid", strconv.FormatInt(folderID, 10))
	params.Set("comment", comment)
	var out struct {
		envelope
		Link string `json:"link"`
		Code string `json:"code"`
	}
	if err := c.call(ctx, "createuploadlink", params, &out); err != nil {
		return "", err
	}
	if out.Link == "" {
		return "", fmt.Errorf("pcloud createuploadlink: empty link in response")
	}
	return out.Link, nil
}

// OAuthToken is the result of exchanging an authorization code for an access
// token.
type OAuthToken struct {
	AccessToken string
	UID         int64
}

// ExchangeOAuthCode trades an OAuth authorization code for an access token at
// the given API host (which must match the user's region, as reported by the
// authorize callback's locationid/hostname). The client_secret is sent in the
// POST body, never the URL query, so it cannot leak into server or proxy logs.
//
// It is a package-level function rather than a Client method because there is
// no token yet at this point in the flow. Pass a nil *http.Client to use the
// default.
func ExchangeOAuthCode(ctx context.Context, httpc *http.Client, host, clientID, clientSecret, code string) (*OAuthToken, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	if host == "" || clientID == "" || clientSecret == "" || code == "" {
		return nil, fmt.Errorf("pcloud oauth2_token: missing host, client credentials, or code")
	}
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("code", code)

	endpoint := "https://" + host + "/oauth2_token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("pcloud oauth2_token: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pcloud oauth2_token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pcloud oauth2_token: read body: %w", err)
	}

	var out struct {
		envelope
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		// pCloud is inconsistent across endpoints about uid vs userid; accept
		// either so the user id is captured wherever it lands.
		UID    int64 `json:"uid"`
		UserID int64 `json:"userid"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("pcloud oauth2_token: decode: %w", err)
	}
	if out.Result != 0 {
		return nil, &APIError{Result: out.Result, Message: out.Error, Method: "oauth2_token"}
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("pcloud oauth2_token: empty access token in response")
	}
	uid := out.UID
	if uid == 0 {
		uid = out.UserID
	}
	return &OAuthToken{AccessToken: out.AccessToken, UID: uid}, nil
}
