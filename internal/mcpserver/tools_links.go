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

// --- list_links / delete_link ---

// ListLinksInput takes no parameters.
type ListLinksInput struct{}

// LinkInfo is one existing public link and its target.
type LinkInfo struct {
	LinkID    int64  `json:"link_id"`
	Link      string `json:"link"`
	Name      string `json:"name"`
	FileID    int64  `json:"file_id,omitempty"`
	FolderID  int64  `json:"folder_id,omitempty"`
	IsFolder  bool   `json:"is_folder"`
	Downloads int64  `json:"downloads"`
	Created   string `json:"created,omitempty"`
}

// ListLinksOutput is the account's existing public links.
type ListLinksOutput struct {
	Links []LinkInfo `json:"links"`
	Total int        `json:"total"`
}

// ListLinks lists the account's existing public (download) links.
func (s *Server) ListLinks(ctx context.Context, _ *mcp.CallToolRequest, _ ListLinksInput) (*mcp.CallToolResult, ListLinksOutput, error) {
	pls, err := s.client.ListPubLinks(ctx)
	if err != nil {
		return nil, ListLinksOutput{}, err
	}
	out := ListLinksOutput{Total: len(pls)}
	for _, p := range pls {
		li := LinkInfo{
			LinkID:    p.LinkID,
			Link:      p.Link,
			Name:      p.Metadata.Name,
			IsFolder:  p.Metadata.IsFolder,
			Downloads: p.Downloads,
			Created:   p.Created,
		}
		if p.Metadata.IsFolder {
			li.FolderID = p.Metadata.FolderID
		} else {
			li.FileID = p.Metadata.FileID
		}
		out.Links = append(out.Links, li)
	}
	return nil, out, nil
}

// DeleteLinkInput selects the public link to revoke.
type DeleteLinkInput struct {
	LinkID int64 `json:"link_id" jsonschema:"public link id to revoke (from pcloud_list_links)"`
}

// DeleteLink revokes a public (download) link.
func (s *Server) DeleteLink(ctx context.Context, _ *mcp.CallToolRequest, in DeleteLinkInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeletePubLink(ctx, in.LinkID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}

// --- create_upload_link ---

// CreateUploadLinkInput selects the folder uploads will land in.
type CreateUploadLinkInput struct {
	FolderID int64  `json:"folder_id" jsonschema:"pCloud folder id that uploads will land in; use 0 for the account root"`
	Comment  string `json:"comment,omitempty" jsonschema:"optional note shown to whoever opens the upload page"`
}

// CreateUploadLink creates a public, anonymous upload link into a folder.
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

// --- list_upload_links / delete_upload_link ---

// ListUploadLinksInput takes no parameters.
type ListUploadLinksInput struct{}

// UploadLinkInfo is one existing upload link and its target folder.
type UploadLinkInfo struct {
	UploadLinkID int64  `json:"upload_link_id"`
	Link         string `json:"link"`
	Comment      string `json:"comment,omitempty"`
	FolderID     int64  `json:"folder_id"`
	FolderName   string `json:"folder_name"`
	Files        int64  `json:"files"`
	Created      string `json:"created,omitempty"`
}

// ListUploadLinksOutput is the account's existing upload links.
type ListUploadLinksOutput struct {
	Links []UploadLinkInfo `json:"links"`
	Total int              `json:"total"`
}

// ListUploadLinks lists the account's existing upload links.
func (s *Server) ListUploadLinks(ctx context.Context, _ *mcp.CallToolRequest, _ ListUploadLinksInput) (*mcp.CallToolResult, ListUploadLinksOutput, error) {
	uls, err := s.client.ListUploadLinks(ctx)
	if err != nil {
		return nil, ListUploadLinksOutput{}, err
	}
	out := ListUploadLinksOutput{Total: len(uls)}
	for _, u := range uls {
		out.Links = append(out.Links, UploadLinkInfo{
			UploadLinkID: u.UploadLinkID,
			Link:         u.Link,
			Comment:      u.Comment,
			FolderID:     u.Metadata.FolderID,
			FolderName:   u.Metadata.Name,
			Files:        u.Files,
			Created:      u.Created,
		})
	}
	return nil, out, nil
}

// DeleteUploadLinkInput selects the upload link to delete.
type DeleteUploadLinkInput struct {
	UploadLinkID int64 `json:"upload_link_id" jsonschema:"upload link id to delete (from pcloud_list_upload_links)"`
}

// DeleteUploadLink deletes an upload link, closing that anonymous write path.
func (s *Server) DeleteUploadLink(ctx context.Context, _ *mcp.CallToolRequest, in DeleteUploadLinkInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.DeleteUploadLink(ctx, in.UploadLinkID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}
