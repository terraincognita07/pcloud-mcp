package download

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

// redirectTransport sends every request — pCloud API calls and CDN downloads
// alike — to a single test server, regardless of the host in the URL. It lets
// the tests drive a real pcloud.Client through its public API with no live
// network and no test-only seam baked into the client.
type redirectTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return t.rt.RoundTrip(r)
}

func testClient(t *testing.T, srv *httptest.Server) *pcloud.Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: redirectTransport{base: u, rt: http.DefaultTransport}}
	return pcloud.New("tok", pcloud.RegionUS, pcloud.WithHTTPClient(hc))
}

// fileBytes is the canned content the fake CDN returns, keyed by fileid.
var fileBytes = map[string]string{
	"10": "aaa",
	"11": "bbbbb",
}

// benignServer answers getfilelink with a link back to itself and serves the
// canned bytes for each fileid.
func benignServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getfilelink"):
			r.ParseForm()
			id := r.PostForm.Get("fileid")
			io.WriteString(w, `{"result":0,"hosts":["cdn.example"],"path":"/dl/`+id+`"}`)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			id := strings.TrimPrefix(r.URL.Path, "/dl/")
			io.WriteString(w, fileBytes[id])
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
}

// TestFile_PartialDownloadCleanedUp is the regression test for the "a failed
// download never leaves truncated data behind" invariant. The fake CDN promises
// more bytes via Content-Length than it actually sends, then closes — the client
// sees an unexpected EOF mid-copy, and the partial file must be removed.
func TestFile_PartialDownloadCleanedUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getfilelink"):
			io.WriteString(w, `{"result":0,"hosts":["cdn.example"],"path":"/dl/x"}`)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			// Claim 100 bytes, send 3, then hijack-close so io.Copy fails.
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("aaa"))
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	base := t.TempDir()
	d := New(testClient(t, srv), base)

	_, err := d.File(context.Background(), &pcloud.Metadata{Name: "broken.bin", FileID: 1, Size: 100})
	if err == nil {
		t.Fatal("expected an error from a truncated download")
	}
	// The partial file must NOT exist.
	if _, statErr := os.Stat(filepath.Join(base, "broken.bin")); !os.IsNotExist(statErr) {
		t.Errorf("partial file was left behind (stat err: %v); it must be removed", statErr)
	}
}

func TestFolder_DownloadsTree(t *testing.T) {
	srv := benignServer(t)
	defer srv.Close()
	base := t.TempDir()
	d := New(testClient(t, srv), base)

	root := &pcloud.Metadata{
		Name: "root", IsFolder: true, FolderID: 1,
		Contents: []pcloud.Metadata{
			{Name: "a.txt", IsFolder: false, FileID: 10, Size: 3},
			{Name: "sub", IsFolder: true, FolderID: 2, Contents: []pcloud.Metadata{
				{Name: "b.txt", IsFolder: false, FileID: 11, Size: 5},
			}},
		},
	}
	stats, err := d.Folder(context.Background(), root)
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	if stats.Files != 2 || stats.Bytes != 8 {
		t.Errorf("stats = %+v; want 2 files / 8 bytes", stats)
	}
	if got := readFile(t, filepath.Join(base, "a.txt")); got != "aaa" {
		t.Errorf("a.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(base, "sub", "b.txt")); got != "bbbbb" {
		t.Errorf("sub/b.txt = %q", got)
	}
}

// TestFolder_BlocksTraversalEndToEnd is the headline security test: a shared
// tree whose folders are named ".." must be refused, nothing may be written
// outside base, and no download may even be attempted.
func TestFolder_BlocksTraversalEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("download attempted for a malicious tree: %s", r.URL.Path)
	}))
	defer srv.Close()

	parent := t.TempDir()
	base := filepath.Join(parent, "MyBackup")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	d := New(testClient(t, srv), base)

	// The exact attack from the Python reference server: folders named ".."
	// chained down to a payload file.
	root := &pcloud.Metadata{
		Name: "shared", IsFolder: true, FolderID: 1,
		Contents: []pcloud.Metadata{
			{Name: "..", IsFolder: true, FolderID: 2, Contents: []pcloud.Metadata{
				{Name: "..", IsFolder: true, FolderID: 3, Contents: []pcloud.Metadata{
					{Name: ".bashrc", IsFolder: false, FileID: 99, Size: 4},
				}},
			}},
		},
	}
	_, err := d.Folder(context.Background(), root)
	if err == nil {
		t.Fatal("expected traversal to be refused, got nil error")
	}
	if !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("error should explain the unsafe name: %v", err)
	}
	// Nothing must have escaped into the parent directory.
	if _, statErr := os.Stat(filepath.Join(parent, ".bashrc")); !os.IsNotExist(statErr) {
		t.Error(".bashrc escaped base into the parent directory")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != "MyBackup" {
		t.Errorf("parent dir was modified: %v", entries)
	}
}

func TestFile_Single(t *testing.T) {
	srv := benignServer(t)
	defer srv.Close()
	base := t.TempDir()
	d := New(testClient(t, srv), base)

	stats, err := d.File(context.Background(), &pcloud.Metadata{Name: "a.txt", FileID: 10, Size: 3})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if stats.Files != 1 || stats.Bytes != 3 {
		t.Errorf("stats = %+v", stats)
	}
	if readFile(t, filepath.Join(base, "a.txt")) != "aaa" {
		t.Error("content mismatch")
	}
}

func TestFile_RejectsUnsafeName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not hit network for an unsafe name: %s", r.URL.Path)
	}))
	defer srv.Close()
	d := New(testClient(t, srv), t.TempDir())
	if _, err := d.File(context.Background(), &pcloud.Metadata{Name: "../evil", FileID: 1}); err == nil {
		t.Error("expected rejection of unsafe single-file name")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}
