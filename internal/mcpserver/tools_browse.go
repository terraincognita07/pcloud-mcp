package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- list_folder ---

// ListFolderInput selects a folder and an optional page window.
type ListFolderInput struct {
	FolderID int64 `json:"folder_id" jsonschema:"pCloud folder id; use 0 for the account root"`
	Offset   int   `json:"offset,omitempty" jsonschema:"number of entries to skip, for paging through a large folder; default 0"`
	Limit    int   `json:"limit,omitempty" jsonschema:"max entries to return; default 200, max 1000. Large folders are paged: read next_offset/has_more and call again to continue"`
}

// ListFolderOutput is one page of a folder's children plus paging metadata.
type ListFolderOutput struct {
	FolderID   int64   `json:"folder_id"`
	Name       string  `json:"name"`
	Entries    []Entry `json:"entries"`
	Total      int     `json:"total"`                 // total children in the folder
	Offset     int     `json:"offset"`                // offset of the first returned entry
	HasMore    bool    `json:"has_more"`              // true if more entries remain past this page
	NextOffset int     `json:"next_offset,omitempty"` // offset to pass next to continue paging
}

// ListFolder returns one page of a folder's immediate children.
func (s *Server) ListFolder(ctx context.Context, _ *mcp.CallToolRequest, in ListFolderInput) (*mcp.CallToolResult, ListFolderOutput, error) {
	md, err := s.client.ListFolder(ctx, in.FolderID, false)
	if err != nil {
		return nil, ListFolderOutput{}, err
	}
	start, end, hasMore, nextOffset := paginate(len(md.Contents), in.Offset, in.Limit)
	out := ListFolderOutput{
		FolderID:   in.FolderID,
		Name:       md.Name,
		Total:      len(md.Contents),
		Offset:     start,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}
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
	return nil, out, nil
}

// --- account_info ---

// AccountInfoInput takes no parameters.
type AccountInfoInput struct{}

// AccountInfoOutput is the authenticated user's account summary.
type AccountInfoOutput struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	UserID        int64  `json:"user_id"`
	QuotaBytes    int64  `json:"quota_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	Premium       bool   `json:"premium"`
}

// AccountInfo returns the authenticated user's account information.
func (s *Server) AccountInfo(ctx context.Context, _ *mcp.CallToolRequest, _ AccountInfoInput) (*mcp.CallToolResult, AccountInfoOutput, error) {
	ui, err := s.client.GetUserInfo(ctx)
	if err != nil {
		return nil, AccountInfoOutput{}, err
	}
	return nil, AccountInfoOutput{
		Email:         ui.Email,
		EmailVerified: ui.EmailVerified,
		UserID:        ui.UserID,
		QuotaBytes:    ui.Quota,
		UsedBytes:     ui.UsedQuota,
		Premium:       ui.Premium,
	}, nil
}

// --- file_info ---

// FileInfoInput selects the file to inspect.
type FileInfoInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id to inspect (from pcloud_list_folder)"`
}

// FileInfoOutput is a file's metadata and content hashes.
type FileInfoOutput struct {
	FileID      int64  `json:"file_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	Created     string `json:"created,omitempty"`
	Modified    string `json:"modified,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SHA1        string `json:"sha1,omitempty"`
	MD5         string `json:"md5,omitempty"`
}

// FileInfo returns a file's metadata and content hashes without downloading it.
func (s *Server) FileInfo(ctx context.Context, _ *mcp.CallToolRequest, in FileInfoInput) (*mcp.CallToolResult, FileInfoOutput, error) {
	cs, err := s.client.ChecksumFile(ctx, in.FileID)
	if err != nil {
		return nil, FileInfoOutput{}, err
	}
	m := cs.Metadata
	return nil, FileInfoOutput{
		FileID:      m.FileID,
		Name:        m.Name,
		Size:        m.Size,
		ContentType: m.ContentType,
		Created:     m.Created,
		Modified:    m.Modified,
		SHA256:      cs.SHA256,
		SHA1:        cs.SHA1,
		MD5:         cs.MD5,
	}, nil
}
