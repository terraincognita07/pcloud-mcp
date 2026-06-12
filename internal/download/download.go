// Package download writes pCloud files and folder trees to the local disk
// inside a fixed base directory, refusing any remote name that would escape it.
//
// This is where the server's headline hardening lives end to end. The names in
// a pCloud listing are attacker-controlled — a shared folder may legitimately
// be named ".." — so the download is contained in two independent layers:
//
//  1. internal/safepath validates every remote name lexically (no traversal
//     tokens, separators, NUL, or reserved names) before it is used, and aborts
//     the whole download on the first bad name. This gives clear errors and
//     fails closed.
//  2. all file and directory I/O goes through an *os.Root scoped to the base
//     directory, so the operating system itself refuses any path that resolves
//     outside base — including via a symlink planted between validation and
//     write (a TOCTOU race that lexical validation alone cannot close).
//
// No remote name is ever URL-decoded into a path component, and the local
// filename always comes from the listing's literal name field, never from a CDN
// link path.
package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
// individual write goes through an *os.Root scoped to it, so nothing the API
// returns can move a write outside this directory.
type Downloader struct {
	client *pcloud.Client
	base   string
}

// New returns a Downloader rooted at base.
func New(client *pcloud.Client, base string) *Downloader {
	return &Downloader{client: client, base: filepath.Clean(base)}
}

// openRoot ensures the base directory exists and returns an *os.Root scoped to
// it. Every subsequent file and directory operation is performed through this
// root, so the kernel enforces containment regardless of what names the remote
// listing contains.
func (d *Downloader) openRoot() (*os.Root, error) {
	if err := os.MkdirAll(d.base, 0o750); err != nil {
		return nil, fmt.Errorf("create base %s: %w", d.base, err)
	}
	root, err := os.OpenRoot(d.base)
	if err != nil {
		return nil, fmt.Errorf("open base %s: %w", d.base, err)
	}
	return root, nil
}

// File downloads a single file (described by meta) into the base directory,
// using its literal name as the local filename.
func (d *Downloader) File(ctx context.Context, meta *pcloud.Metadata) (Stats, error) {
	if _, err := safepath.SafeName(meta.Name); err != nil {
		return Stats{}, err
	}
	root, err := d.openRoot()
	if err != nil {
		return Stats{}, err
	}
	defer root.Close()

	n, err := d.downloadFile(ctx, root, meta, meta.Name)
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
	osRoot, err := d.openRoot()
	if err != nil {
		return Stats{}, err
	}
	defer osRoot.Close()

	var s Stats
	if err := d.walk(ctx, osRoot, root, "", &s); err != nil {
		return s, err
	}
	return s, nil
}

func (d *Downloader) walk(ctx context.Context, root *os.Root, node *pcloud.Metadata, rel string, s *Stats) error {
	for i := range node.Contents {
		child := node.Contents[i]
		// Lexical gate: validate the name before it is ever used as a path
		// component. os.Root enforces containment a second time at the syscall.
		if _, err := safepath.SafeName(child.Name); err != nil {
			return fmt.Errorf("download aborted at %q: %w", child.Name, err)
		}
		childRel := filepath.Join(rel, child.Name)

		if child.IsFolder {
			// Parent dirs are always created before their children by this
			// top-down walk, so a single-level Mkdir is sufficient.
			if err := root.Mkdir(childRel, 0o750); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create dir %s: %w", childRel, err)
			}
			if err := d.walk(ctx, root, &child, childRel, s); err != nil {
				return err
			}
		} else {
			n, err := d.downloadFile(ctx, root, &child, childRel)
			if err != nil {
				return err
			}
			s.Files++
			s.Bytes += n
		}
	}
	return nil
}

// downloadFile resolves a direct link for meta and streams it to root/rel. All
// I/O goes through the root, so neither rel nor the temp file can escape base.
//
// The write is atomic: bytes land in an exclusive temp file beside the target,
// which is renamed over rel only after the transfer completes. A failed
// transfer therefore removes only its own temp file — it can never truncate or
// delete a file that already existed at rel (re-downloading over an old copy is
// safe even when the network drops mid-stream).
func (d *Downloader) downloadFile(ctx context.Context, root *os.Root, meta *pcloud.Metadata, rel string) (int64, error) {
	link, err := d.client.GetFileLink(ctx, meta.FileID, true)
	if err != nil {
		return 0, err
	}
	tmp, f, err := createTemp(root, rel)
	if err != nil {
		return 0, err
	}
	n, err := d.client.Download(ctx, link, f)
	closeErr := f.Close()
	if err != nil {
		_ = root.Remove(tmp) // drop the partial temp; rel itself is untouched
		return 0, err
	}
	if closeErr != nil {
		_ = root.Remove(tmp)
		return 0, fmt.Errorf("close %s: %w", tmp, closeErr)
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return 0, fmt.Errorf("commit %s: %w", rel, err)
	}
	return n, nil
}

// createTemp opens an exclusive (O_EXCL) temp file next to rel — same
// directory, so the final rename cannot cross a filesystem boundary and stays
// atomic. The suffix is random so the temp name cannot collide with a real
// remote name in the same listing; O_EXCL turns any remaining collision into a
// retry instead of an overwrite.
func createTemp(root *os.Root, rel string) (string, *os.File, error) {
	for range 4 {
		suffix, err := randomSuffix()
		if err != nil {
			return "", nil, err
		}
		name := rel + ".partial-" + suffix
		f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, fmt.Errorf("open temp for %s: %w", rel, err)
		}
	}
	return "", nil, fmt.Errorf("open temp for %s: name collisions persist", rel)
}

// randomSuffix returns 8 hex chars of crypto-grade randomness for temp names.
func randomSuffix() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("temp name randomness: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
