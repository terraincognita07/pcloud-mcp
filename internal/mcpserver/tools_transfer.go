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

// --- download_file / download_folder ---

// DownloadFileInput selects a file and a local destination directory.
type DownloadFileInput struct {
	FileID      int64  `json:"file_id" jsonschema:"pCloud file id to download (from pcloud_list_folder)"`
	Name        string `json:"name" jsonschema:"the file's name (from pcloud_list_folder); used as the local filename"`
	Destination string `json:"destination" jsonschema:"absolute local directory to save the file into"`
}

// DownloadResult summarises a completed download.
type DownloadResult struct {
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	Path  string `json:"path"`
}

// DownloadFile downloads one file into a local directory.
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

// DownloadFolderInput selects a folder tree and a local destination directory.
type DownloadFolderInput struct {
	FolderID    int64  `json:"folder_id" jsonschema:"pCloud folder id to download (from pcloud_list_folder)"`
	Name        string `json:"name" jsonschema:"the folder's name (from pcloud_list_folder); the tree is mirrored under destination/<name>"`
	Destination string `json:"destination" jsonschema:"absolute local directory to mirror the folder into"`
}

// DownloadFolder mirrors a folder tree into a local directory.
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

// UploadFileInput selects a local file and a pCloud destination.
type UploadFileInput struct {
	LocalPath string `json:"local_path" jsonschema:"absolute path to the local file to upload"`
	FolderID  int64  `json:"folder_id" jsonschema:"destination pCloud folder id; use 0 for the account root"`
	Name      string `json:"name,omitempty" jsonschema:"optional name to store the file as; defaults to the local file name"`
}

// UploadResult identifies an uploaded (or text-saved) file.
type UploadResult struct {
	FileID int64  `json:"file_id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}

// UploadFile uploads a local file into a pCloud folder.
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

// --- upload_from_url ---

// UploadFromURLInput is a remote URL and the folder pCloud should fetch it into.
type UploadFromURLInput struct {
	URL      string `json:"url" jsonschema:"the remote URL pCloud should fetch into the folder"`
	FolderID int64  `json:"folder_id" jsonschema:"destination folder id; 0 = account root"`
}

// UploadFromURLOutput lists the files pCloud fetched.
type UploadFromURLOutput struct {
	Files []Entry `json:"files"`
}

// UploadFromURL has pCloud fetch a remote URL straight into a folder.
func (s *Server) UploadFromURL(ctx context.Context, _ *mcp.CallToolRequest, in UploadFromURLInput) (*mcp.CallToolResult, UploadFromURLOutput, error) {
	if in.URL == "" {
		return nil, UploadFromURLOutput{}, fmt.Errorf("url is required")
	}
	mds, err := s.client.UploadFromURL(ctx, in.URL, in.FolderID)
	if err != nil {
		return nil, UploadFromURLOutput{}, err
	}
	out := UploadFromURLOutput{}
	for _, md := range mds {
		out.Files = append(out.Files, Entry{Name: md.Name, ID: md.FileID, IsFolder: false, Size: md.Size, ContentType: md.ContentType})
	}
	return nil, out, nil
}

// --- get_zip_link / get_media_link ---

// GetZipLinkInput selects the folder to zip.
type GetZipLinkInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id to download as a zip; 0 = account root"`
}

// GetZipLink returns a temporary URL to download a folder as a zip archive.
func (s *Server) GetZipLink(ctx context.Context, _ *mcp.CallToolRequest, in GetZipLinkInput) (*mcp.CallToolResult, LinkResult, error) {
	link, err := s.client.GetZipLink(ctx, in.FolderID)
	if err != nil {
		return nil, LinkResult{}, err
	}
	return nil, LinkResult{Link: link}, nil
}

// GetMediaLinkInput selects a media file and the stream kind.
type GetMediaLinkInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id of a video or audio file"`
	Audio  bool  `json:"audio,omitempty" jsonschema:"set true for an audio stream (getaudiolink); default false = video"`
}

// GetMediaLink returns a temporary streaming URL for a video or audio file.
func (s *Server) GetMediaLink(ctx context.Context, _ *mcp.CallToolRequest, in GetMediaLinkInput) (*mcp.CallToolResult, LinkResult, error) {
	link, err := s.client.GetStreamLink(ctx, in.FileID, in.Audio)
	if err != nil {
		return nil, LinkResult{}, err
	}
	return nil, LinkResult{Link: link}, nil
}
