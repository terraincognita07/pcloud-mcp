package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/safepath"
)

// --- create_folder ---

// CreateFolderInput names a new folder and its parent.
type CreateFolderInput struct {
	ParentID int64  `json:"parent_id" jsonschema:"parent pCloud folder id; use 0 for the account root"`
	Name     string `json:"name" jsonschema:"name of the new folder"`
}

// FolderResult identifies a created or modified folder.
type FolderResult struct {
	FolderID int64  `json:"folder_id"`
	Name     string `json:"name"`
}

// CreateFolder creates a folder under parent_id.
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

// --- delete_file / delete_folder ---

// DeleteFileInput selects the file to delete.
type DeleteFileInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id to delete (moved to pCloud's time-limited Trash)"`
}

// DeleteFile deletes a file (pCloud routes it to Trash).
func (s *Server) DeleteFile(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeleteFile(ctx, in.FileID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}

// DeleteFolderInput selects the folder to delete recursively.
type DeleteFolderInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id to delete, including all contents (moved to pCloud's time-limited Trash)"`
}

// DeleteFolder deletes a folder and all of its contents recursively.
func (s *Server) DeleteFolder(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFolderInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeleteFolderRecursive(ctx, in.FolderID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}

// --- move_file / move_folder ---

// MoveFileInput renames and/or moves a file. ToFolderID is a pointer because 0
// is a real destination (the account root) — only an omitted field means "keep
// the file in its current folder".
type MoveFileInput struct {
	FileID     int64  `json:"file_id" jsonschema:"pCloud file id to rename and/or move"`
	ToFolderID *int64 `json:"to_folder_id,omitempty" jsonschema:"destination folder id; use 0 for the account root; omit to keep the file in its current folder"`
	NewName    string `json:"new_name,omitempty" jsonschema:"new name; omit to keep the current name"`
}

// MoveFile renames and/or moves a file.
func (s *Server) MoveFile(ctx context.Context, _ *mcp.CallToolRequest, in MoveFileInput) (*mcp.CallToolResult, Entry, error) {
	if in.ToFolderID == nil && in.NewName == "" {
		return nil, Entry{}, fmt.Errorf("provide new_name, to_folder_id, or both")
	}
	md, err := s.client.RenameFile(ctx, in.FileID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FileID, IsFolder: false, Size: md.Size, ContentType: md.ContentType}, nil
}

// MoveFolderInput renames and/or moves a folder. ToFolderID is a pointer for
// the same reason as MoveFileInput's: 0 is the account root, not "unset".
type MoveFolderInput struct {
	FolderID   int64  `json:"folder_id" jsonschema:"pCloud folder id to rename and/or move"`
	ToFolderID *int64 `json:"to_folder_id,omitempty" jsonschema:"destination parent folder id; use 0 for the account root; omit to keep the folder where it is"`
	NewName    string `json:"new_name,omitempty" jsonschema:"new name; omit to keep the current name"`
}

// MoveFolder renames and/or moves a folder.
func (s *Server) MoveFolder(ctx context.Context, _ *mcp.CallToolRequest, in MoveFolderInput) (*mcp.CallToolResult, Entry, error) {
	if in.ToFolderID == nil && in.NewName == "" {
		return nil, Entry{}, fmt.Errorf("provide new_name, to_folder_id, or both")
	}
	md, err := s.client.RenameFolder(ctx, in.FolderID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FolderID, IsFolder: true}, nil
}

// --- copy_file / copy_folder ---

// CopyFileInput selects a file and its copy destination.
type CopyFileInput struct {
	FileID     int64  `json:"file_id" jsonschema:"pCloud file id to copy"`
	ToFolderID int64  `json:"to_folder_id" jsonschema:"destination folder id; use 0 for the account root"`
	NewName    string `json:"new_name,omitempty" jsonschema:"optional name for the copy; omit to keep the original name"`
}

// CopyFile copies a file into another folder, leaving the original in place.
func (s *Server) CopyFile(ctx context.Context, _ *mcp.CallToolRequest, in CopyFileInput) (*mcp.CallToolResult, Entry, error) {
	md, err := s.client.CopyFile(ctx, in.FileID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FileID, IsFolder: false, Size: md.Size, ContentType: md.ContentType}, nil
}

// CopyFolderInput selects a folder and its copy destination.
type CopyFolderInput struct {
	FolderID   int64  `json:"folder_id" jsonschema:"pCloud folder id to copy (with all its contents)"`
	ToFolderID int64  `json:"to_folder_id" jsonschema:"destination folder id; use 0 for the account root"`
	NewName    string `json:"new_name,omitempty" jsonschema:"optional name for the copy; omit to keep the original name"`
}

// CopyFolder copies a folder and its contents, leaving the original in place.
func (s *Server) CopyFolder(ctx context.Context, _ *mcp.CallToolRequest, in CopyFolderInput) (*mcp.CallToolResult, Entry, error) {
	md, err := s.client.CopyFolder(ctx, in.FolderID, in.ToFolderID, in.NewName)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FolderID, IsFolder: true}, nil
}

// --- save_text ---

// SaveTextInput is text content to write into a new pCloud file.
type SaveTextInput struct {
	FolderID int64  `json:"folder_id" jsonschema:"destination pCloud folder id; use 0 for the account root"`
	Name     string `json:"name" jsonschema:"file name to create, e.g. note.md"`
	Content  string `json:"content" jsonschema:"the text content to write into the file"`
}

// SaveText writes text straight into a new pCloud file.
func (s *Server) SaveText(ctx context.Context, _ *mcp.CallToolRequest, in SaveTextInput) (*mcp.CallToolResult, UploadResult, error) {
	// The name becomes a pCloud filename; validate it as a single safe component
	// so it cannot smuggle a path ("../x") or separator into the upload.
	if _, err := safepath.SafeName(in.Name); err != nil {
		return nil, UploadResult{}, err
	}
	md, err := s.client.UploadFile(ctx, in.FolderID, in.Name, strings.NewReader(in.Content), int64(len(in.Content)))
	if err != nil {
		return nil, UploadResult{}, err
	}
	return nil, UploadResult{FileID: md.FileID, Name: md.Name, Size: md.Size}, nil
}
