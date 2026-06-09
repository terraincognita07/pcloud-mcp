package safepath

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSafeName_Valid(t *testing.T) {
	valid := []string{
		"report.pdf",
		"My Backup",
		"file.tar.gz",
		"scom1",         // not reserved, just starts like COM1
		"naïve-Ünïcödé", // non-ASCII is fine
		"a.b.c",
		"%2F-looks-encoded-but-is-literal", // a real name may contain a percent
	}
	for _, name := range valid {
		if got, err := SafeName(name); err != nil || got != name {
			t.Errorf("SafeName(%q) = %q, %v; want %q, nil", name, got, err, name)
		}
	}
}

func TestSafeName_Rejected(t *testing.T) {
	bad := []string{
		"",
		".",
		"..",
		"a/b",           // unix separator
		`a\b`,           // windows separator
		"/etc/passwd",   // absolute-ish, separators
		`C:\Windows`,    // drive letter (colon) + separator
		"stream:name",   // NTFS alternate data stream
		"trailing.",     // trailing dot (Windows strips it)
		"trailing ",     // trailing space (Windows strips it)
		"   ",           // only spaces
		"...",           // collapses to empty after trim
		"with\x00nul",   // NUL byte
		"with\tcontrol", // control character
		"CON",           // reserved device name
		"con.txt",       // reserved, with extension, lower-case
		"NUL",
		"LpT9.log", // reserved, mixed case
	}
	for _, name := range bad {
		if _, err := SafeName(name); err == nil {
			t.Errorf("SafeName(%q) = nil error; want rejection", name)
		}
	}
}

func TestJoin_StaysInsideBase(t *testing.T) {
	base := filepath.Join("download", "root")
	got, err := Join(base, "sub", "deeper", "file.txt")
	if err != nil {
		t.Fatalf("Join returned error: %v", err)
	}
	want := filepath.Join(base, "sub", "deeper", "file.txt")
	if got != want {
		t.Errorf("Join = %q; want %q", got, want)
	}
}

// TestJoin_BlocksTraversal reproduces the exact attack from the Python
// reference server: a shared folder structure whose names are "..", walking the
// download out of the base directory. Every variant must be refused.
func TestJoin_BlocksTraversal(t *testing.T) {
	base := filepath.Join("home", "user", "Downloads", "MyBackup")
	attacks := [][]string{
		{".."},
		{"..", "..", "tmp", ".bashrc"},    // the classic chain
		{"sub", "..", "..", "..", "evil"}, // escape after a valid prefix
		{"/etc", "cron.d", "evil"},        // separator smuggled in a name
		{`..\..\Windows`},                 // windows-style traversal
	}
	for _, parts := range attacks {
		if _, err := Join(base, parts...); err == nil {
			t.Errorf("Join(base, %v) = nil error; want rejection", parts)
		}
	}
}

// TestJoin_DecodedSeparatorIsCaught documents the getfilelink %2F case: the
// caller must URL-decode BEFORE validating. A decoded separator is then caught
// by SafeName, whereas the still-encoded literal is a legitimate single name.
func TestJoin_DecodedSeparatorIsCaught(t *testing.T) {
	base := "out"
	if _, err := Join(base, "etc/passwd"); err == nil { // already decoded
		t.Error("Join with decoded separator should be rejected")
	}
	if _, err := Join(base, "etc%2Fpasswd"); err != nil { // still encoded = literal
		t.Errorf("Join with literal %%2F name should pass, got %v", err)
	}
}

func TestSafeName_ErrorIsTyped(t *testing.T) {
	_, err := SafeName("..")
	if err == nil || !errors.Is(err, ErrUnsafeName) {
		t.Errorf("expected ErrUnsafeName-wrapped error, got %v", err)
	}
}
