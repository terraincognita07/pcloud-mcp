package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

// ShareResult is the URL of a created link plus its id (for later revoke).
type ShareResult struct {
	Link   string `json:"link"`
	LinkID int64  `json:"link_id,omitempty"` // use with pcloud_delete_link to revoke
}

// linkOptions builds a pcloud.LinkOptions from the optional share inputs.
func linkOptions(expire, password string, maxDownloads int64) pcloud.LinkOptions {
	return pcloud.LinkOptions{Expire: expire, Password: password, MaxDownloads: maxDownloads}
}

// --- share_file ---

// ShareFileInput selects a file and optional public-link settings.
type ShareFileInput struct {
	FileID       int64  `json:"file_id" jsonschema:"pCloud file id to create a public link for"`
	ExpireDate   string `json:"expire_date,omitempty" jsonschema:"optional expiry as 'YYYY-MM-DD HH:MM:SS' (UTC); omit for a link that never expires"`
	Password     string `json:"password,omitempty" jsonschema:"optional password required to open the link"`
	MaxDownloads int64  `json:"max_downloads,omitempty" jsonschema:"optional maximum number of downloads before the link stops working"`
}

// ShareFile creates a public link for a file.
func (s *Server) ShareFile(ctx context.Context, _ *mcp.CallToolRequest, in ShareFileInput) (*mcp.CallToolResult, ShareResult, error) {
	res, err := s.client.GetFilePubLink(ctx, in.FileID, linkOptions(in.ExpireDate, in.Password, in.MaxDownloads))
	if err != nil {
		return nil, ShareResult{}, err
	}
	return nil, ShareResult{Link: res.Link, LinkID: res.LinkID}, nil
}

// --- share_folder ---

// ShareFolderInput selects a folder and optional public-link settings.
type ShareFolderInput struct {
	FolderID     int64  `json:"folder_id" jsonschema:"pCloud folder id to create a public link for"`
	ExpireDate   string `json:"expire_date,omitempty" jsonschema:"optional expiry as 'YYYY-MM-DD HH:MM:SS' (UTC); omit for a link that never expires"`
	Password     string `json:"password,omitempty" jsonschema:"optional password required to open the link"`
	MaxDownloads int64  `json:"max_downloads,omitempty" jsonschema:"optional maximum number of downloads before the link stops working"`
}

// ShareFolder creates a public link for an entire folder.
func (s *Server) ShareFolder(ctx context.Context, _ *mcp.CallToolRequest, in ShareFolderInput) (*mcp.CallToolResult, ShareResult, error) {
	res, err := s.client.GetFolderPubLink(ctx, in.FolderID, linkOptions(in.ExpireDate, in.Password, in.MaxDownloads))
	if err != nil {
		return nil, ShareResult{}, err
	}
	return nil, ShareResult{Link: res.Link, LinkID: res.LinkID}, nil
}

// --- delete_link ---

// DeleteLinkInput selects the public link to revoke.
type DeleteLinkInput struct {
	LinkID int64 `json:"link_id" jsonschema:"public link id to revoke (returned by pcloud_share_file / pcloud_share_folder)"`
}

// DeleteLink revokes a public (download) link.
func (s *Server) DeleteLink(ctx context.Context, _ *mcp.CallToolRequest, in DeleteLinkInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeletePubLink(ctx, in.LinkID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}
