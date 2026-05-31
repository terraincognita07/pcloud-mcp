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
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// Register adds every pCloud tool to m.
func (s *Server) Register(m *mcp.Server) {
	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_folder",
		Description: "List the immediate contents of a pCloud folder. Use folder_id 0 for the account root. Returns each child's name, id, and whether it is a folder.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListFolder)

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

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_create_folder",
		Description: "Create a new folder inside a pCloud parent folder. Use parent_id 0 for the account root.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(true)},
	}, s.CreateFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_file",
		Description: "Permanently delete a pCloud file by file_id. This cannot be undone.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)},
	}, s.DeleteFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_folder",
		Description: "Permanently delete a pCloud folder by folder_id, including ALL of its contents, recursively. This cannot be undone.",
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

type ListFolderInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id; use 0 for the account root"`
}

type ListFolderOutput struct {
	FolderID int64   `json:"folder_id"`
	Name     string  `json:"name"`
	Entries  []Entry `json:"entries"`
}

func (s *Server) ListFolder(ctx context.Context, _ *mcp.CallToolRequest, in ListFolderInput) (*mcp.CallToolResult, ListFolderOutput, error) {
	md, err := s.client.ListFolder(ctx, in.FolderID, false)
	if err != nil {
		return nil, ListFolderOutput{}, err
	}
	out := ListFolderOutput{FolderID: in.FolderID, Name: md.Name}
	for _, c := range md.Contents {
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
	return nil, out, nil
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
	FileID int64 `json:"file_id" jsonschema:"pCloud file id to permanently delete"`
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
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id to permanently delete, including all contents"`
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
