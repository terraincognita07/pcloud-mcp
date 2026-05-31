// Package config persists the pCloud OAuth credentials on local disk.
//
// The token is a long-lived, full-access credential, so the file is created
// with 0600 permissions (owner-only) via an atomic write: a temp file in the
// same directory is written, permissioned, and renamed into place, so a reader
// never observes a half-written or briefly world-readable file. The token is
// never logged — Credentials.String redacts it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials is what we persist after a successful OAuth exchange.
type Credentials struct {
	AccessToken string `json:"access_token"`
	Region      int    `json:"locationid"` // pCloud locationid: 1=US, 2=EU
	UID         int64  `json:"uid,omitempty"`
}

// String redacts the token so Credentials is safe to print in diagnostics.
func (c Credentials) String() string {
	return fmt.Sprintf("Credentials{region:%d, uid:%d, token:<redacted>}", c.Region, c.UID)
}

// DefaultPath returns the per-user credentials path
// (<os.UserConfigDir>/pcloud-mcp/credentials.json).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "pcloud-mcp", "credentials.json"), nil
}

// Save writes c to path atomically with owner-only permissions, creating the
// parent directory if needed.
func Save(path string, c *Credentials) error {
	if c.AccessToken == "" {
		return errors.New("refusing to save credentials with empty access token")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}

	// Write to a temp file in the same dir, then rename — rename within a
	// directory is atomic, so a concurrent reader sees either the old file or
	// the complete new one, never a partial write.
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit credentials: %w", err)
	}
	return nil
}

// Load reads and validates credentials from path.
func Load(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode credentials: %w", err)
	}
	if c.AccessToken == "" {
		return nil, errors.New("credentials file has no access token")
	}
	return &c, nil
}
