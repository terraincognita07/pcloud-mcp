package pcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient wires a Client at an httptest server's URL so requests never
// leave the process.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("tok-secret", RegionUS, withBaseURL(srv.URL))
}

func TestRegionHost(t *testing.T) {
	if got := RegionUS.apiHost(); got != "api.pcloud.com" {
		t.Errorf("US host = %q", got)
	}
	if got := RegionEU.apiHost(); got != "eapi.pcloud.com" {
		t.Errorf("EU host = %q", got)
	}
	if got := Region(99).apiHost(); got != "api.pcloud.com" {
		t.Errorf("unknown region should default to US, got %q", got)
	}
}

// TestTokenInBodyNotURL is the security-relevant assertion: the access token must
// travel in the POST body, never in the request URL.
func TestTokenInBodyNotURL(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "tok-secret") {
			t.Errorf("token leaked into URL query: %q", r.URL.RawQuery)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.PostForm.Get("access_token") != "tok-secret" {
			t.Errorf("token not found in POST body; got %v", r.PostForm)
		}
		io.WriteString(w, `{"result":0,"metadata":{"name":"root","isfolder":true}}`)
	})
	if _, err := c.ListFolder(context.Background(), 0, false); err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
}

func TestStringRedactsToken(t *testing.T) {
	c := New("super-secret-token", RegionUS)
	if strings.Contains(c.String(), "super-secret-token") {
		t.Errorf("String() leaked the token: %s", c.String())
	}
}

func TestAPIErrorMapped(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":2094,"error":"Invalid 'access_token' provided."}`)
	})
	_, err := c.ListFolder(context.Background(), 0, false)
	if err == nil {
		t.Fatal("expected APIError, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Result != 2094 {
		t.Errorf("Result = %d; want 2094", apiErr.Result)
	}
	if !strings.Contains(apiErr.Error(), "listfolder") {
		t.Errorf("error should name the method: %v", apiErr)
	}
}

func TestListFolderRecursiveTree(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.PostFormValue("recursive") != "1" {
			t.Errorf("recursive flag not sent")
		}
		io.WriteString(w, `{
			"result":0,
			"metadata":{
				"name":"root","isfolder":true,"folderid":1,
				"contents":[
					{"name":"a.txt","isfolder":false,"fileid":10,"size":3},
					{"name":"sub","isfolder":true,"folderid":2,"contents":[
						{"name":"b.txt","isfolder":false,"fileid":11,"size":5}
					]}
				]
			}
		}`)
	})
	md, err := c.ListFolder(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if len(md.Contents) != 2 {
		t.Fatalf("expected 2 children, got %d", len(md.Contents))
	}
	sub := md.Contents[1]
	if !sub.IsFolder || sub.FolderID != 2 || len(sub.Contents) != 1 {
		t.Errorf("subfolder not parsed: %+v", sub)
	}
	if sub.Contents[0].FileID != 11 {
		t.Errorf("nested file id = %d; want 11", sub.Contents[0].FileID)
	}
}

func TestGetFileLinkBuildsURL(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"hosts":["c1.pcloud.com","c2.pcloud.com"],"path":"/x/My%20file.jpg"}`)
	})
	got, err := c.GetFileLink(context.Background(), 42, true)
	if err != nil {
		t.Fatalf("GetFileLink: %v", err)
	}
	want := "https://c1.pcloud.com/x/My%20file.jpg"
	if got != want {
		t.Errorf("GetFileLink = %q; want %q", got, want)
	}
}

// TestBuildDownloadURL_RejectsHostConfusion is the regression test for the
// malicious-upstream finding: a path or host crafted to move the authority off
// the real CDN host must be refused.
func TestBuildDownloadURL_RejectsHostConfusion(t *testing.T) {
	bad := []struct{ host, path string }{
		{"c1.pcloud.com", "@evil.com/x"}, // userinfo smuggled via path
		{"c1.pcloud.com", "evil.com/x"},  // no leading slash
		{"c1.pcloud.com@evil.com", "/x"}, // userinfo in host
		{"evil.com/c1.pcloud.com", "/x"}, // separator in host
		{"c1.pcloud.com?x=1", "/y"},      // query smuggled in host
		{"c1.pcloud.com ", "/y"},         // trailing space
	}
	for _, c := range bad {
		if got, err := buildDownloadURL(c.host, c.path); err == nil {
			t.Errorf("buildDownloadURL(%q,%q) = %q; want rejection", c.host, c.path, got)
		}
	}
	// A normal response still works, path left percent-encoded.
	got, err := buildDownloadURL("c63.pcloud.com", "/dl/My%20file.jpg")
	if err != nil {
		t.Fatalf("benign URL rejected: %v", err)
	}
	if got != "https://c63.pcloud.com/dl/My%20file.jpg" {
		t.Errorf("benign URL = %q", got)
	}
}

func TestGetFileLinkEmptyHostsErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"hosts":[],"path":""}`)
	})
	if _, err := c.GetFileLink(context.Background(), 42, false); err == nil {
		t.Error("expected error on empty hosts/path")
	}
}

func TestDownloadStreams(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "file-bytes")
	})
	// Download hits an absolute URL; point it at the same test server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "file-bytes")
	}))
	t.Cleanup(srv.Close)
	var buf strings.Builder
	n, err := c.Download(context.Background(), srv.URL+"/whatever", &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if buf.String() != "file-bytes" || n != int64(len("file-bytes")) {
		t.Errorf("Download got %q (n=%d)", buf.String(), n)
	}
}

func TestUploadFileMultipart(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("not multipart: %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("access_token") != "tok-secret" {
			t.Errorf("token missing in multipart body")
		}
		if r.FormValue("folderid") != "7" {
			t.Errorf("folderid = %q; want 7", r.FormValue("folderid"))
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer f.Close()
		if hdr.Filename != "note.txt" {
			t.Errorf("filename = %q", hdr.Filename)
		}
		content, _ := io.ReadAll(f)
		if string(content) != "hello" {
			t.Errorf("content = %q", content)
		}
		io.WriteString(w, `{"result":0,"metadata":[{"name":"note.txt","fileid":99,"size":5}]}`)
	})
	md, err := c.UploadFile(context.Background(), 7, "note.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if md.FileID != 99 {
		t.Errorf("returned fileid = %d; want 99", md.FileID)
	}
}

func TestRenameFileSendsParams(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("fileid") != "5" {
			t.Errorf("fileid = %q", r.PostForm.Get("fileid"))
		}
		if r.PostForm.Get("tofolderid") != "8" {
			t.Errorf("tofolderid = %q", r.PostForm.Get("tofolderid"))
		}
		if r.PostForm.Get("toname") != "renamed.txt" {
			t.Errorf("toname = %q", r.PostForm.Get("toname"))
		}
		io.WriteString(w, `{"result":0,"metadata":{"name":"renamed.txt","fileid":5}}`)
	})
	if _, err := c.RenameFile(context.Background(), 5, 8, "renamed.txt"); err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
}

func TestGetFilePubLink(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"link":"https://my.pcloud.com/publink/show?code=ABC"}`)
	})
	link, err := c.GetFilePubLink(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetFilePubLink: %v", err)
	}
	if !strings.Contains(link, "publink") {
		t.Errorf("link = %q", link)
	}
}

func TestCreateUploadLink(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("folderid") != "12" {
			t.Errorf("folderid = %q; want 12", r.PostForm.Get("folderid"))
		}
		if r.PostForm.Get("comment") != "drop files here" {
			t.Errorf("comment = %q", r.PostForm.Get("comment"))
		}
		io.WriteString(w, `{"result":0,"uploadlinkid":7,"code":"ABC","link":"https://my.pcloud.com/#page=puplink&code=ABC"}`)
	})
	link, err := c.CreateUploadLink(context.Background(), 12, "drop files here")
	if err != nil {
		t.Fatalf("CreateUploadLink: %v", err)
	}
	if !strings.Contains(link, "puplink") {
		t.Errorf("link = %q", link)
	}
}

func TestCreateUploadLinkEmptyLinkErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"link":""}`)
	})
	if _, err := c.CreateUploadLink(context.Background(), 1, "x"); err == nil {
		t.Error("expected error on empty link")
	}
}

func TestExchangeOAuthCode(t *testing.T) {
	var sawSecretInURL bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "shh-secret") {
			sawSecretInURL = true
		}
		r.ParseForm()
		if r.PostForm.Get("client_secret") != "shh-secret" {
			t.Errorf("client_secret not in body: %v", r.PostForm)
		}
		if r.PostForm.Get("code") != "auth-code" {
			t.Errorf("code = %q", r.PostForm.Get("code"))
		}
		io.WriteString(w, `{"result":0,"access_token":"AT","token_type":"bearer","uid":777}`)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	// ExchangeOAuthCode hardcodes https; point it back at the test server via a
	// transport that rewrites scheme+host.
	hc := &http.Client{Transport: rewriteTransport{host: host}}
	tok, err := ExchangeOAuthCode(context.Background(), hc, host, "cid", "shh-secret", "auth-code")
	if err != nil {
		t.Fatalf("ExchangeOAuthCode: %v", err)
	}
	if tok.AccessToken != "AT" || tok.UID != 777 {
		t.Errorf("token = %+v", tok)
	}
	if sawSecretInURL {
		t.Error("client_secret leaked into URL query")
	}
}

func TestExchangeOAuthCode_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":2012,"error":"Invalid code."}`)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	hc := &http.Client{Transport: rewriteTransport{host: host}}
	if _, err := ExchangeOAuthCode(context.Background(), hc, host, "cid", "sec", "bad"); err == nil {
		t.Error("expected APIError for bad code")
	}
}

// rewriteTransport forces every request to the test server, letting us exercise
// the https-only ExchangeOAuthCode against httptest's http listener.
type rewriteTransport struct{ host string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = rt.host
	return http.DefaultTransport.RoundTrip(r)
}

// TestEnvelopeDecodes guards the assumption that result+payload coexist in one
// JSON object (pCloud does not nest the payload under a "data" key).
func TestEnvelopeDecodes(t *testing.T) {
	var out struct {
		envelope
		Metadata Metadata `json:"metadata"`
	}
	raw := `{"result":0,"metadata":{"name":"x","isfolder":false,"fileid":3}}`
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Result != 0 || out.Metadata.FileID != 3 {
		t.Errorf("decoded = %+v", out)
	}
}
