// Package download writes pCloud files and folder trees to the local disk
// inside a fixed base directory, refusing any remote name that would escape it.
//
// This is where the server's headline hardening lives end to end. The names in
// a pCloud listing are attacker-controlled — a shared folder may legitimately
// be named ".." — so every local path is built through internal/safepath, which
// rejects traversal tokens and separators before a single byte is written.
// Unlike the reference Python implementation, no remote name is ever
// URL-decoded into a path component, and the local filename always comes from
// the listing's literal name field, never from a CDN link path.
package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
	"github.com/terraincognita07/pcloud-mcp/internal/safepath"
)

// Stats summarises a completed download.
type Stats struct {
	Files int
	Bytes int64
}

// Downloader writes into base. base is cleaned once at construction; every
// individual write is re-checked against it by safepath, so nothing the API
// returns can move a write outside this directory.
type Downloader struct {
	client *pcloud.Client
	base   string
}

// New returns a Downloader rooted at base.
func New(client *pcloud.Client, base string) *Downloader {
	return &Downloader{client: client, base: filepath.Clean(base)}
}

// File downloads a single file (described by meta) into the base directory,
// using its literal name as the local filename.
func (d *Downloader) File(ctx context.Context, meta *pcloud.Metadata) (Stats, error) {
	n, err := d.downloadFile(ctx, meta, []string{meta.Name})
	if err != nil {
		return Stats{}, err
	}
	return Stats{Files: 1, Bytes: n}, nil
}

// Folder walks the (recursively listed) tree rooted at root and downloads every
// file under base, mirroring the remote structure. It fails closed: the first
// unsafe name aborts the whole download rather than skipping the item, so a
// malicious tree can never partially land on disk.
func (d *Downloader) Folder(ctx context.Context, root *pcloud.Metadata) (Stats, error) {
	var s Stats
	if err := d.walk(ctx, root, nil, &s); err != nil {
		return s, err
	}
	return s, nil
}

func (d *Downloader) walk(ctx context.Context, node *pcloud.Metadata, rel []string, s *Stats) error {
	for i := range node.Contents {
		child := node.Contents[i]
		// Validate the name before it is ever used as a path component.
		if _, err := safepath.SafeName(child.Name); err != nil {
			return fmt.Errorf("download aborted at %q: %w", child.Name, err)
		}
		childRel := append(append([]string(nil), rel...), child.Name)

		if child.IsFolder {
			dir, err := safepath.Join(d.base, childRel...)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
			if err := d.walk(ctx, &child, childRel, s); err != nil {
				return err
			}
		} else {
			n, err := d.downloadFile(ctx, &child, childRel)
			if err != nil {
				return err
			}
			s.Files++
			s.Bytes += n
		}
	}
	return nil
}

// downloadFile resolves a direct link for meta and streams it to base/rel,
// validating the full path first. A partial file is removed if the transfer
// fails, so a failed download never leaves truncated data behind.
func (d *Downloader) downloadFile(ctx context.Context, meta *pcloud.Metadata, rel []string) (int64, error) {
	dest, err := safepath.Join(d.base, rel...)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}
	link, err := d.client.GetFileLink(ctx, meta.FileID, true)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", dest, err)
	}
	n, err := d.client.Download(ctx, link, f)
	closeErr := f.Close()
	if err != nil {
		os.Remove(dest) // drop the partial file
		return 0, err
	}
	if closeErr != nil {
		os.Remove(dest)
		return 0, fmt.Errorf("close %s: %w", dest, closeErr)
	}
	return n, nil
}
