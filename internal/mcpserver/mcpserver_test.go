package mcpserver

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

// redirectTransport forces every request to the test server regardless of host,
// so a real pcloud.Client can be exercised with no network.
type redirectTransport struct{ base *url.URL }

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newServer(t *testing.T, h http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	hc := &http.Client{Transport: redirectTransport{base: u}}
	return New(pcloud.New("tok", pcloud.RegionUS, pcloud.WithHTTPClient(hc))), srv
}

func TestListFolder(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"metadata":{"name":"root","folderid":0,"isfolder":true,"contents":[
			{"name":"doc.pdf","isfolder":false,"fileid":5,"size":99,"contenttype":"application/pdf"},
			{"name":"Photos","isfolder":true,"folderid":7}
		]}}`)
	})
	_, out, err := s.ListFolder(context.Background(), nil, ListFolderInput{FolderID: 0})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("entries = %d; want 2", len(out.Entries))
	}
	if out.Entries[0].ID != 5 || out.Entries[0].Size != 99 || out.Entries[0].IsFolder {
		t.Errorf("file entry wrong: %+v", out.Entries[0])
	}
	if out.Entries[1].ID != 7 || !out.Entries[1].IsFolder {
		t.Errorf("folder entry wrong: %+v", out.Entries[1])
	}
}

func TestDownloadFile(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getfilelink"):
			io.WriteString(w, `{"result":0,"hosts":["cdn"],"path":"/dl/x"}`)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			io.WriteString(w, "payload")
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	dest := t.TempDir()
	_, out, err := s.DownloadFile(context.Background(), nil, DownloadFileInput{FileID: 1, Name: "x.txt", Destination: dest})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if out.Files != 1 || out.Bytes != 7 {
		t.Errorf("result = %+v", out)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "x.txt"))
	if string(b) != "payload" {
		t.Errorf("file content = %q", b)
	}
}

// TestDownloadFolder_RejectsTraversalName is the MCP-layer guard: a folder whose
// own name is ".." must be refused before any network call, and nothing may be
// created outside the destination.
func TestDownloadFolder_RejectsTraversalName(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no network call should happen for an unsafe folder name: %s", r.URL.Path)
	})
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.DownloadFolder(context.Background(), nil, DownloadFolderInput{FolderID: 1, Name: "..", Destination: dest})
	if err == nil {
		t.Fatal("expected rejection of '..' folder name")
	}
	// Nothing escaped into the parent dir.
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != "dest" {
		t.Errorf("parent dir modified: %v", entries)
	}
}

func TestUploadFile(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		_, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Filename != "up.txt" {
			t.Errorf("filename = %q", hdr.Filename)
		}
		io.WriteString(w, `{"result":0,"metadata":[{"name":"up.txt","fileid":55,"size":4}]}`)
	})
	local := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(local, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, err := s.UploadFile(context.Background(), nil, UploadFileInput{LocalPath: local, FolderID: 0})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if out.FileID != 55 || out.Name != "up.txt" {
		t.Errorf("result = %+v", out)
	}
}

func TestUploadFile_MissingLocalFile(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not call API when local file is missing")
	})
	_, _, err := s.UploadFile(context.Background(), nil, UploadFileInput{LocalPath: filepath.Join(t.TempDir(), "nope.txt")})
	if err == nil {
		t.Error("expected error for missing local file")
	}
}

func TestDeleteFolder(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/deletefolderrecursive") {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		io.WriteString(w, `{"result":0}`)
	})
	_, out, err := s.DeleteFolder(context.Background(), nil, DeleteFolderInput{FolderID: 9})
	if err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if !out.Deleted {
		t.Error("expected Deleted=true")
	}
}

func TestMoveFile_RequiresAnArgument(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not call API with no rename/move target")
	})
	if _, _, err := s.MoveFile(context.Background(), nil, MoveFileInput{FileID: 1}); err == nil {
		t.Error("expected error when neither new_name nor to_folder_id is set")
	}
}

func TestShareFile(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"link":"https://my.pcloud.com/publink/show?code=XYZ"}`)
	})
	_, out, err := s.ShareFile(context.Background(), nil, ShareFileInput{FileID: 1})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if !strings.Contains(out.Link, "publink") {
		t.Errorf("link = %q", out.Link)
	}
}

func TestSaveText(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if hdr.Filename != "note.md" {
			t.Errorf("filename = %q", hdr.Filename)
		}
		body, _ := io.ReadAll(f)
		if string(body) != "hello world" {
			t.Errorf("content = %q", body)
		}
		io.WriteString(w, `{"result":0,"metadata":[{"name":"note.md","fileid":42,"size":11}]}`)
	})
	_, out, err := s.SaveText(context.Background(), nil, SaveTextInput{FolderID: 0, Name: "note.md", Content: "hello world"})
	if err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if out.FileID != 42 {
		t.Errorf("result = %+v", out)
	}
}

// TestSaveText_RejectsUnsafeName is the security check: a file name that tries to
// traverse must be refused before any API call.
func TestSaveText_RejectsUnsafeName(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("must not call API for an unsafe name: %s", r.URL.Path)
	})
	if _, _, err := s.SaveText(context.Background(), nil, SaveTextInput{Name: "../evil", Content: "x"}); err == nil {
		t.Error("expected rejection of traversal name in save_text")
	}
}

func TestCreateUploadLink(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("folderid") != "3" {
			t.Errorf("folderid = %q", r.PostForm.Get("folderid"))
		}
		if r.PostForm.Get("comment") == "" {
			t.Error("comment must be non-empty (pCloud requires it)")
		}
		io.WriteString(w, `{"result":0,"uploadlinkid":9,"code":"Z","link":"https://my.pcloud.com/#page=puplink&code=Z"}`)
	})
	_, out, err := s.CreateUploadLink(context.Background(), nil, CreateUploadLinkInput{FolderID: 3})
	if err != nil {
		t.Fatalf("CreateUploadLink: %v", err)
	}
	if !strings.Contains(out.Link, "puplink") {
		t.Errorf("link = %q", out.Link)
	}
}

func TestAPIErrorPropagates(t *testing.T) {
	s, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":2005,"error":"Directory does not exist."}`)
	})
	if _, _, err := s.ListFolder(context.Background(), nil, ListFolderInput{FolderID: 123}); err == nil {
		t.Error("expected API error to propagate to the tool handler")
	}
}
