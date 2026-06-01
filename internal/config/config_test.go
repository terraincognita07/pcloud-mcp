package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	// Nested path also exercises parent-directory creation.
	path := filepath.Join(t.TempDir(), "sub", "credentials.json")
	in := &Credentials{AccessToken: "secret-tok", Region: 2, UID: 42}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != *in {
		t.Errorf("round trip = %+v; want %+v", *out, *in)
	}
}

func TestSaveRejectsEmptyToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := Save(path, &Credentials{Region: 1}); err == nil {
		t.Error("expected error saving empty token")
	}
}

func TestLoadRejectsMissingToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"locationid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error loading tokenless file")
	}
}

func TestFilePermsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "c.json")
	if err := Save(path, &Credentials{AccessToken: "x", Region: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o; want 600", perm)
	}
}

func TestStringRedactsToken(t *testing.T) {
	c := Credentials{AccessToken: "super-secret", Region: 1}
	if strings.Contains(c.String(), "super-secret") {
		t.Errorf("String leaked token: %s", c.String())
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if path == "" {
		t.Fatal("DefaultPath returned empty path")
	}
	// Must end with our app dir + file, regardless of OS config root.
	want := filepath.Join("pcloud-mcp", "credentials.json")
	if !strings.HasSuffix(path, want) {
		t.Errorf("DefaultPath = %q; want suffix %q", path, want)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("DefaultPath = %q; want an absolute path", path)
	}
}
