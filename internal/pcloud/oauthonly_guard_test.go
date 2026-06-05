package pcloud_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenAuthTokens are substrings that only show up when a username/password
// login flow is (re)introduced. This project is OAuth-only by design: the Python
// original leaked the password in a URL query, and "hardened, OAuth-only" is the
// whole value proposition (see AI_CONTEXT.md / CLAUDE.md -> "OAuth only — never
// add a username/password flow"). pCloud's password endpoints are getauth /
// getdigest (with a passworddigest).
//
// This guard is deliberately airtight, not clean: a false positive (e.g. a
// share-link "password" that is NOT an auth flow) is acceptable and must be
// resolved by a human narrowing this list on purpose. A false negative — a
// password flow slipping in unnoticed — is not acceptable.
//
// The bare token "password" was deliberately removed once pcloud_share_file /
// pcloud_share_folder gained pCloud's link-password feature (the API param is
// "linkpassword"), which is a public-link option, not an account login. A real
// username/password auth flow is still caught here: it cannot exist without one
// of getauth / userauth / getdigest / passworddigest, and it needs a username.
// The non-vacuous guard test plants exactly such a flow and still trips on
// getauth + username.
var forbiddenAuthTokens = []string{
	"getauth",
	"getdigest",
	"passworddigest",
	"userauth",
	"username",
}

// scanForForbiddenAuthTokens walks root and returns a finding ("token in relpath")
// for every forbidden token in a shipped (non-test) .go file. Test files are
// skipped: they don't ship, and this guard's own token list lives in a _test.go.
func scanForForbiddenAuthTokens(root string) ([]string, error) {
	var findings []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hay := strings.ToLower(string(data))
		for _, tok := range forbiddenAuthTokens {
			if strings.Contains(hay, tok) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				findings = append(findings, tok+" in "+filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return findings, err
}

// TestOAuthOnly_NoPasswordFlow is the hard enforcement of the OAuth-only
// invariant: it scans every shipped (non-test) .go file in the module and fails
// if any username/password-auth token appears. It runs under `go test ./...`
// (make check) and the CI "build · vet · test" required check, so a password
// flow cannot be merged.
func TestOAuthOnly_NoPasswordFlow(t *testing.T) {
	findings, err := scanForForbiddenAuthTokens(repoRoot(t))
	if err != nil {
		t.Fatalf("scanning module source: %v", err)
	}
	for _, f := range findings {
		t.Errorf("forbidden auth token %s: this project is OAuth-only — never add a "+
			"username/password flow (AI_CONTEXT.md / CLAUDE.md). If this is a "+
			"deliberate non-auth use, narrow forbiddenAuthTokens on purpose.", f)
	}
}

// TestOAuthOnly_GuardDetectsPasswordFlow proves the guard is not vacuous: it
// plants a file that reintroduces a pCloud password endpoint and asserts the
// scanner flags it. Without this, a bug that silently matched nothing would let
// a real password flow through unnoticed.
func TestOAuthOnly_GuardDetectsPasswordFlow(t *testing.T) {
	dir := t.TempDir()
	planted := "package evil\n\nvar _ = \"https://api.pcloud.com/getauth?username=x&password=y\"\n"
	if err := os.WriteFile(filepath.Join(dir, "passwordlogin.go"), []byte(planted), 0o600); err != nil {
		t.Fatalf("planting file: %v", err)
	}
	findings, err := scanForForbiddenAuthTokens(dir)
	if err != nil {
		t.Fatalf("scanning planted tree: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("guard did not flag a planted password-auth flow — the scan is vacuous")
	}
}

// repoRoot returns the module root by walking up from the test's working
// directory (the package dir, under `go test`) until it finds go.mod. Using the
// working dir keeps this independent of -trimpath.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}
