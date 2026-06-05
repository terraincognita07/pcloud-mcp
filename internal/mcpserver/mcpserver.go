// Package mcpserver exposes the pCloud client as a set of MCP tools.
//
// Each tool is a thin, typed handler over the already-tested internal packages:
// pcloud (HTTP client), download (filesystem-bounded writes), and safepath (the
// traversal guard). Destructive and outward-facing tools are tagged with the
// matching MCP ToolAnnotations so a client can surface the risk to the user
// before the call is approved.
//
// Tools are methods on Server so they can be unit-tested directly with an
// httptest-backed pcloud.Client, without standing up an MCP transport.
package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/download"
	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
	"github.com/terraincognita07/pcloud-mcp/internal/safepath"
)

// Server holds the dependencies shared by every tool handler.
type Server struct {
	client *pcloud.Client
}

// New returns a Server backed by client.
func New(client *pcloud.Client) *Server {
	return &Server{client: client}
}

// boolPtr is a helper for the *bool annotation fields.
func boolPtr(b bool) *bool { return &b }

// Mode selects which tools are exposed.
type Mode int

const (
	// ModeLocal exposes every tool, including the ones that read and write the
	// local filesystem (download_*, upload_file). It is for the stdio transport,
	// where the server runs on the user's own machine.
	ModeLocal Mode = iota
	// ModeRemote hides the local-filesystem tools. Over the HTTP transport the
	// server runs on a host (a VPS), so download_*/upload_file would touch the
	// server's disk, not the user's — exposing them would be confusing and would
	// let a request write to the server. Cloud-side tools stay available.
	ModeRemote
)

// Register adds the local-mode tool set (all tools) to m. It is kept for the
// stdio entrypoint and for tests.
func (s *Server) Register(m *mcp.Server) { s.RegisterMode(m, ModeLocal) }

// RegisterMode adds the pCloud tools appropriate for mode. Local-filesystem
// tools are only registered in ModeLocal.
func (s *Server) RegisterMode(m *mcp.Server, mode Mode) {
	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_folder",
		Description: "List the immediate contents of a pCloud folder. Use folder_id 0 for the account root. Returns each child's name, id, and whether it is a folder.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_get_thumbnail",
		Description: "Fetch a small JPEG thumbnail of a pCloud image or video by file_id and return it inline so the model can see it. size is WIDTHxHEIGHT (default 256x256, max 1024x1024). Use this to visually scan or identify photos cheaply without pulling full-resolution files; it also works for formats the model can't read directly (e.g. BMP), since pCloud renders the preview as JPEG.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.GetThumbnail)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_read_file",
		Description: "Read a pCloud file by file_id and return its content inline: text files (md/json/txt/code) as text, viewable images (JPEG/PNG/GIF/WebP) as an image the model can see. Files larger than max_bytes (default 5 MiB, max 10 MiB) or non-text/non-image binaries return a temporary download link instead of content, so a large file never overflows the context. Works in both stdio and HTTP modes.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ReadFile)

	if mode == ModeLocal {
		mcp.AddTool(m, &mcp.Tool{
			Name:        "pcloud_download_file",
			Description: "Download a single pCloud file to a local directory. Provide file_id and the file's name (both from pcloud_list_folder) and a local destination directory. The name is validated to prevent path traversal.",
			Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
		}, s.DownloadFile)

		mcp.AddTool(m, &mcp.Tool{
			Name:        "pcloud_download_folder",
			Description: "Download a pCloud folder and its entire subtree to a local directory, mirroring the structure under destination/<name>. Every remote name is validated to prevent path traversal; the download aborts if any name is unsafe.",
			Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
		}, s.DownloadFolder)

		mcp.AddTool(m, &mcp.Tool{
			Name:        "pcloud_upload_file",
			Description: "Upload a local file into a pCloud folder. Use folder_id 0 for the account root. By default the local file name is kept; set name to store under a different name.",
			Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
		}, s.UploadFile)
	}

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_create_folder",
		Description: "Create a new folder inside a pCloud parent folder. Use parent_id 0 for the account root.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, s.CreateFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_file",
		Description: "Delete a pCloud file by file_id. pCloud normally moves deleted items to Trash, recoverable for a limited, plan-dependent period before permanent purge; sharing is not restored. Destructive — do not rely on Trash as a backup.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)},
	}, s.DeleteFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_folder",
		Description: "Delete a pCloud folder by folder_id and ALL of its contents, recursively, removing its sharing. Deleted items normally go to pCloud Trash, restorable for a limited, plan-dependent time before permanent purge; do not rely on recovery. Destructive.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)},
	}, s.DeleteFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_move_file",
		Description: "Rename and/or move a pCloud file. Set new_name to rename, to_folder_id to move; either or both may be provided.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.MoveFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_move_folder",
		Description: "Rename and/or move a pCloud folder. Set new_name to rename, to_folder_id to move; either or both may be provided.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.MoveFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_share_file",
		Description: "Create a public share link for a pCloud file. Anyone with the link can access the file, so confirm intent before sharing.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.ShareFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_save_text",
		Description: "Save text content directly into a new pCloud file (e.g. a note, summary, or generated document) without needing a local file. Provide folder_id (0 = root), a file name, and the text content.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.SaveText)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_create_upload_link",
		Description: "Create a public upload link for a pCloud folder. ANYONE with the returned URL can upload files into that folder without signing in — useful for collecting files from a phone or another person. Confirm intent before creating; it opens an unauthenticated write path into the account.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.CreateUploadLink)
}

// --- Shared output shapes ---

// Entry is a compact view of one folder child.
type Entry struct {
	Name        string `json:"name"`
	ID          int64  `json:"id"` // folderid for folders, fileid for files
	IsFolder    bool   `json:"is_folder"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// --- list_folder ---

// Listing limits keep a single response bounded so a large folder cannot
// overflow the client's context window. pCloud returns the whole folder in one
// call, so the page is sliced server-side here.
const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

type ListFolderInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id; use 0 for the account root"`
	Offset   int   `json:"offset,omitempty" jsonschema:"number of entries to skip, for paging through a large folder; default 0"`
	Limit    int   `json:"limit,omitempty" jsonschema:"max entries to return; default 200, max 1000. Large folders are paged: read next_offset/has_more and call again to continue"`
}

type ListFolderOutput struct {
	FolderID   int64   `json:"folder_id"`
	Name       string  `json:"name"`
	Entries    []Entry `json:"entries"`
	Total      int     `json:"total"`                 // total children in the folder
	Offset     int     `json:"offset"`                // offset of the first returned entry
	HasMore    bool    `json:"has_more"`              // true if more entries remain past this page
	NextOffset int     `json:"next_offset,omitempty"` // offset to pass next to continue paging
}

func (s *Server) ListFolder(ctx context.Context, _ *mcp.CallToolRequest, in ListFolderInput) (*mcp.CallToolResult, ListFolderOutput, error) {
	md, err := s.client.ListFolder(ctx, in.FolderID, false)
	if err != nil {
		return nil, ListFolderOutput{}, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	total := len(md.Contents)
	start := in.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	out := ListFolderOutput{FolderID: in.FolderID, Name: md.Name, Total: total, Offset: start}
	for _, c := range md.Contents[start:end] {
		e := Entry{Name: c.Name, IsFolder: c.IsFolder}
		if c.IsFolder {
			e.ID = c.FolderID
		} else {
			e.ID = c.FileID
			e.Size = c.Size
			e.ContentType = c.ContentType
		}
		out.Entries = append(out.Entries, e)
	}
	if end < total {
		out.HasMore = true
		out.NextOffset = end
	}
	return nil, out, nil
}

// --- get_thumbnail ---

// Thumbnail limits. The size is bounded so a request cannot ask pCloud for a
// huge render, and the fetched bytes are capped so an unexpectedly large body
// cannot blow the response/context budget.
const (
	defaultThumbSize = "256x256"
	minThumbDim      = 16
	maxThumbDim      = 1024
	maxThumbBytes    = 8 << 20 // 8 MiB; real thumbnails are far smaller
)

type GetThumbnailInput struct {
	FileID int64  `json:"file_id" jsonschema:"pCloud file id of an image or video (from pcloud_list_folder)"`
	Size   string `json:"size,omitempty" jsonschema:"thumbnail size as WIDTHxHEIGHT, e.g. 256x256; default 256x256, each dimension 16..1024"`
}

type ThumbnailOutput struct {
	FileID int64  `json:"file_id"`
	Size   string `json:"size"`
	Bytes  int64  `json:"bytes"`
}

func (s *Server) GetThumbnail(ctx context.Context, _ *mcp.CallToolRequest, in GetThumbnailInput) (*mcp.CallToolResult, ThumbnailOutput, error) {
	size, err := normalizeThumbSize(in.Size)
	if err != nil {
		return nil, ThumbnailOutput{}, err
	}
	link, err := s.client.GetThumbLink(ctx, in.FileID, size)
	if err != nil {
		return nil, ThumbnailOutput{}, err
	}
	var buf bytes.Buffer
	n, err := s.client.Download(ctx, link, &cappedWriter{w: &buf, max: maxThumbBytes})
	if err != nil {
		return nil, ThumbnailOutput{}, err
	}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.ImageContent{Data: buf.Bytes(), MIMEType: "image/jpeg"}},
	}
	return result, ThumbnailOutput{FileID: in.FileID, Size: size, Bytes: n}, nil
}

// normalizeThumbSize validates a "WIDTHxHEIGHT" thumbnail size and returns it in
// canonical form. An empty size defaults to defaultThumbSize.
func normalizeThumbSize(size string) (string, error) {
	if size == "" {
		return defaultThumbSize, nil
	}
	w, h, ok := strings.Cut(size, "x")
	if !ok {
		return "", fmt.Errorf("size %q must be WIDTHxHEIGHT, e.g. 256x256", size)
	}
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || wi < minThumbDim || hi < minThumbDim || wi > maxThumbDim || hi > maxThumbDim {
		return "", fmt.Errorf("size %q out of range; each dimension must be %d..%d", size, minThumbDim, maxThumbDim)
	}
	return strconv.Itoa(wi) + "x" + strconv.Itoa(hi), nil
}

// errCapExceeded is returned by cappedWriter when a body grows past its limit,
// so callers can distinguish "too big" (fall back to a link) from a transfer
// error (surface it).
var errCapExceeded = errors.New("size cap exceeded")

// cappedWriter fails the copy if more than max bytes are written, so an
// unexpectedly large body errors out instead of being silently truncated.
type cappedWriter struct {
	w   *bytes.Buffer
	n   int64
	max int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		return 0, errCapExceeded
	}
	c.n += int64(len(p))
	return c.w.Write(p)
}

// --- read_file ---

// Read limits keep an inline response bounded. A file over the cap (or a binary
// the model can't render) comes back as a temporary download link instead of
// bytes, so reading a large file never overflows the context.
const (
	defaultReadCap = 5 << 20  // 5 MiB
	maxReadCap     = 10 << 20 // 10 MiB
)

type ReadFileInput struct {
	FileID   int64 `json:"file_id" jsonschema:"pCloud file id to read (from pcloud_list_folder)"`
	MaxBytes int   `json:"max_bytes,omitempty" jsonschema:"max bytes to pull inline; default 5MiB, hard max 10MiB. A larger file returns a temporary download link instead of content"`
}

type ReadFileOutput struct {
	FileID      int64  `json:"file_id"`
	Kind        string `json:"kind"` // "text", "image", or "link"
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	Link        string `json:"link,omitempty"` // set when kind == "link"
}

func (s *Server) ReadFile(ctx context.Context, _ *mcp.CallToolRequest, in ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
	limit := in.MaxBytes
	if limit <= 0 {
		limit = defaultReadCap
	}
	if limit > maxReadCap {
		limit = maxReadCap
	}

	link, err := s.client.GetFileLink(ctx, in.FileID, false)
	if err != nil {
		return nil, ReadFileOutput{}, err
	}
	var buf bytes.Buffer
	if _, err := s.client.Download(ctx, link, &cappedWriter{w: &buf, max: int64(limit)}); err != nil {
		if errors.Is(err, errCapExceeded) {
			// Too big to inline → hand back the (working) temporary link.
			return nil, ReadFileOutput{FileID: in.FileID, Kind: "link", Link: link}, nil
		}
		return nil, ReadFileOutput{}, err
	}

	data := buf.Bytes()
	ct := http.DetectContentType(data)
	mime := strings.SplitN(ct, ";", 2)[0]
	switch {
	case strings.HasPrefix(ct, "text/") && utf8.Valid(data):
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
		return res, ReadFileOutput{FileID: in.FileID, Kind: "text", ContentType: mime, Bytes: int64(len(data))}, nil
	case isInlineImage(mime):
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: mime}}}
		return res, ReadFileOutput{FileID: in.FileID, Kind: "image", ContentType: mime, Bytes: int64(len(data))}, nil
	default:
		// Binary the model can't render inline → link, not raw bytes.
		return nil, ReadFileOutput{FileID: in.FileID, Kind: "link", ContentType: mime, Bytes: int64(len(data)), Link: link}, nil
	}
}

// isInlineImage reports whether ct is an image format the model can view inline.
func isInlineImage(ct string) bool {
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

// --- download_file ---

type DownloadFileInput struct {
	FileID      int64  `json:"file_id" jsonschema:"pCloud file id to download (from pcloud_list_folder)"`
	Name        string `json:"name" jsonschema:"the file's name (from pcloud_list_folder); used as the local filename"`
	Destination string `json:"destination" jsonschema:"absolute local directory to save the file into"`
}

type DownloadResult struct {
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	Path  string `json:"path"`
}

func (s *Server) DownloadFile(ctx context.Context, _ *mcp.CallToolRequest, in DownloadFileInput) (*mcp.CallToolResult, DownloadResult, error) {
	if in.Destination == "" {
		return nil, DownloadResult{}, fmt.Errorf("destination directory is required")
	}
	d := download.New(s.client, in.Destination)
	stats, err := d.File(ctx, &pcloud.Metadata{Name: in.Name, FileID: in.FileID})
	if err != nil {
		return nil, DownloadResult{}, err
	}
	// safepath already validated Name; rebuilding the path here is display-only.
	return nil, DownloadResult{Files: stats.Files, Bytes: stats.Bytes, Path: filepath.Join(in.Destination, in.Name)}, nil
}

// --- download_folder ---

type DownloadFolderInput struct {
	FolderID    int64  `json:"folder_id" jsonschema:"pCloud folder id to download (from pcloud_list_folder)"`
	Name        string `json:"name" jsonschema:"the folder's name (from pcloud_list_folder); the tree is mirrored under destination/<name>"`
	Destination string `json:"destination" jsonschema:"absolute local directory to mirror the folder into"`
}

func (s *Server) DownloadFolder(ctx context.Context, _ *mcp.CallToolRequest, in DownloadFolderInput) (*mcp.CallToolResult, DownloadResult, error) {
	if in.Destination == "" {
		return nil, DownloadResult{}, fmt.Errorf("destination directory is required")
	}
	// Name is attacker-influenced (it comes from a pCloud listing), so it must
	// pass safepath before being joined onto the trusted destination.
	if _, err := safepath.SafeName(in.Name); err != nil {
		return nil, DownloadResult{}, err
	}
	base := filepath.Join(in.Destination, in.Name)

	tree, err := s.client.ListFolder(ctx, in.FolderID, true)
	if err != nil {
		return nil, DownloadResult{}, err
	}
	d := download.New(s.client, base)
	stats, err := d.Folder(ctx, tree)
	if err != nil {
		return nil, DownloadResult{}, err
	}
	return nil, DownloadResult{Files: stats.Files, Bytes: stats.Bytes, Path: base}, nil
}

// --- upload_file ---

type UploadFileInput struct {
	LocalPath string `json:"local_path" jsonschema:"absolute path to the local file to upload"`
	FolderID  int64  `json:"folder_id" jsonschema:"destination pCloud folder id; use 0 for the account root"`
	Name      string `json:"name,omitempty" jsonschema:"optional name to store the file as; defaults to the local file name"`
}

type UploadResult struct {
	FileID int64  `json:"file_id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}

func (s *Server) UploadFile(ctx context.Context, _ *mcp.CallToolRequest, in UploadFileInput) (*mcp.CallToolResult, UploadResult, error) {
	if in.LocalPath == "" {
		return nil, UploadResult{}, fmt.Errorf("local_path is required")
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(in.LocalPath)
	}
	f, err := os.Open(in.LocalPath)
	if err != nil {
		return nil, UploadResult{}, fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	md, err := s.client.UploadFile(ctx, in.FolderID, name, f)
	if err != nil {
		return nil, UploadResult{}, err
	}
	return nil, UploadResult{FileID: md.FileID, Name: md.Name, Size: md.Size}, nil
}

// --- create_folder ---

type CreateFolderInput struct {
	ParentID int64  `json:"parent_id" jsonschema:"parent pCloud folder id; use 0 for the account root"`
	Name     string `json:"name" jsonschema:"name of the new folder"`
}

type FolderResult struct {
	FolderID int64  `json:"folder_id"`
	Name     string `json:"name"`
}

func (s *Server) CreateFolder(ctx context.Context, _ *mcp.CallToolRequest, in CreateFolderInput) (*mcp.CallToolResult, FolderResult, error) {
	if in.Name == "" {
		return nil, FolderResult{}, fmt.Errorf("folder name is required")
	}
	md, err := s.client.CreateFolder(ctx, in.ParentID, in.Name)
	if err != nil {
		return nil, FolderResult{}, err
	}
	return nil, FolderResult{FolderID: md.FolderID, Name: md.Name}, nil
}

// --- delete_file ---

type DeleteFileInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id to delete (moved to pCloud's time-limited Trash)"`
}

type DeleteResult struct {
	Deleted bool `json:"deleted"`
}

func (s *Server) DeleteFile(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeleteFile(ctx, in.FileID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}

// --- delete_folder ---

type DeleteFolderInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id to delete, including all contents (moved to pCloud's time-limited Trash)"`
}

func (s *Server) DeleteFolder(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFolderInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeleteFolderRecursive(ctx, in.FolderID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}

// --- move_file ---

type MoveFileInput struct {
	FileID     int64  `json:"file_id" jsonschema:"pCloud file id to rename and/or move"`
	ToFolderID int64  `json:"to_folder_id,omitempty" jsonschema:"destination folder id; omit or 0 to keep in place"`
	NewName    string `json:"new_name,omitempty" jsonschema:"new name; omit to keep the current name"`
}

func (s *Server) MoveFile(ctx context.Context, _ *mcp.CallToolRequest, in MoveFileInput) (*mcp.CallToolResult, Entry, error) {
	if in.ToFolderID == 0 && in.NewName == "" {
		return nil, Entry{}, fmt.Errorf("provide new_name, to_folder_id, or both")
	}
	md, err := s.client.RenameFile(ctx, in.FileID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FileID, IsFolder: false, Size: md.Size, ContentType: md.ContentType}, nil
}

// --- move_folder ---

type MoveFolderInput struct {
	FolderID   int64  `json:"folder_id" jsonschema:"pCloud folder id to rename and/or move"`
	ToFolderID int64  `json:"to_folder_id,omitempty" jsonschema:"destination parent folder id; omit or 0 to keep in place"`
	NewName    string `json:"new_name,omitempty" jsonschema:"new name; omit to keep the current name"`
}

func (s *Server) MoveFolder(ctx context.Context, _ *mcp.CallToolRequest, in MoveFolderInput) (*mcp.CallToolResult, Entry, error) {
	if in.ToFolderID == 0 && in.NewName == "" {
		return nil, Entry{}, fmt.Errorf("provide new_name, to_folder_id, or both")
	}
	md, err := s.client.RenameFolder(ctx, in.FolderID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FolderID, IsFolder: true}, nil
}

// --- share_file ---

type ShareFileInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id to create a public link for"`
}

type ShareResult struct {
	Link string `json:"link"`
}

func (s *Server) ShareFile(ctx context.Context, _ *mcp.CallToolRequest, in ShareFileInput) (*mcp.CallToolResult, ShareResult, error) {
	link, err := s.client.GetFilePubLink(ctx, in.FileID)
	if err != nil {
		return nil, ShareResult{}, err
	}
	return nil, ShareResult{Link: link}, nil
}

// --- save_text ---

type SaveTextInput struct {
	FolderID int64  `json:"folder_id" jsonschema:"destination pCloud folder id; use 0 for the account root"`
	Name     string `json:"name" jsonschema:"file name to create, e.g. note.md"`
	Content  string `json:"content" jsonschema:"the text content to write into the file"`
}

func (s *Server) SaveText(ctx context.Context, _ *mcp.CallToolRequest, in SaveTextInput) (*mcp.CallToolResult, UploadResult, error) {
	// The name becomes a pCloud filename; validate it as a single safe component
	// so it cannot smuggle a path ("../x") or separator into the upload.
	if _, err := safepath.SafeName(in.Name); err != nil {
		return nil, UploadResult{}, err
	}
	md, err := s.client.UploadFile(ctx, in.FolderID, in.Name, strings.NewReader(in.Content))
	if err != nil {
		return nil, UploadResult{}, err
	}
	return nil, UploadResult{FileID: md.FileID, Name: md.Name, Size: md.Size}, nil
}

// --- create_upload_link ---

type CreateUploadLinkInput struct {
	FolderID int64  `json:"folder_id" jsonschema:"pCloud folder id that uploads will land in; use 0 for the account root"`
	Comment  string `json:"comment,omitempty" jsonschema:"optional note shown to whoever opens the upload page"`
}

func (s *Server) CreateUploadLink(ctx context.Context, _ *mcp.CallToolRequest, in CreateUploadLinkInput) (*mcp.CallToolResult, ShareResult, error) {
	comment := in.Comment
	if comment == "" {
		comment = "Upload files here" // pCloud requires a non-empty comment
	}
	link, err := s.client.CreateUploadLink(ctx, in.FolderID, comment)
	if err != nil {
		return nil, ShareResult{}, err
	}
	return nil, ShareResult{Link: link}, nil
}
