package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- list_trash ---

// ListTrashInput selects a Trash folder and an optional page window.
type ListTrashInput struct {
	FolderID int64 `json:"folder_id,omitempty" jsonschema:"Trash subfolder id; 0 = Trash root"`
	Offset   int   `json:"offset,omitempty" jsonschema:"entries to skip, for paging; default 0"`
	Limit    int   `json:"limit,omitempty" jsonschema:"max entries to return; default 200, max 1000"`
}

// TrashEntry is one item in the Trash, with where it lived before deletion.
type TrashEntry struct {
	Name               string `json:"name"`
	ID                 int64  `json:"id"`
	IsFolder           bool   `json:"is_folder"`
	OrigParentFolderID int64  `json:"orig_parent_folder_id,omitempty"`
}

// ListTrashOutput is one page of Trash contents plus paging metadata.
type ListTrashOutput struct {
	Entries    []TrashEntry `json:"entries"`
	Total      int          `json:"total"`
	Offset     int          `json:"offset"`
	HasMore    bool         `json:"has_more"`
	NextOffset int          `json:"next_offset,omitempty"`
}

// ListTrash returns one page of the account's Trash.
func (s *Server) ListTrash(ctx context.Context, _ *mcp.CallToolRequest, in ListTrashInput) (*mcp.CallToolResult, ListTrashOutput, error) {
	md, err := s.client.TrashList(ctx, in.FolderID, false)
	if err != nil {
		return nil, ListTrashOutput{}, err
	}
	start, end, hasMore, nextOffset := paginate(len(md.Contents), in.Offset, in.Limit)
	out := ListTrashOutput{Total: len(md.Contents), Offset: start, HasMore: hasMore, NextOffset: nextOffset}
	for _, c := range md.Contents[start:end] {
		e := TrashEntry{Name: c.Name, IsFolder: c.IsFolder, OrigParentFolderID: c.OrigParentFolderID}
		if c.IsFolder {
			e.ID = c.FolderID
		} else {
			e.ID = c.FileID
		}
		out.Entries = append(out.Entries, e)
	}
	return nil, out, nil
}

// --- restore_from_trash ---

// RestoreFromTrashInput selects a trashed file or folder to restore.
type RestoreFromTrashInput struct {
	FileID    int64 `json:"file_id,omitempty" jsonschema:"file id to restore (from pcloud_list_trash); set this or folder_id"`
	FolderID  int64 `json:"folder_id,omitempty" jsonschema:"folder id to restore (from pcloud_list_trash); set this or file_id"`
	RestoreTo int64 `json:"restore_to,omitempty" jsonschema:"optional destination folder id; omit to restore to the original location"`
}

// RestoreFromTrash restores a file or folder out of the Trash.
func (s *Server) RestoreFromTrash(ctx context.Context, _ *mcp.CallToolRequest, in RestoreFromTrashInput) (*mcp.CallToolResult, Entry, error) {
	if in.FileID == 0 && in.FolderID == 0 {
		return nil, Entry{}, fmt.Errorf("file_id or folder_id is required")
	}
	md, err := s.client.TrashRestore(ctx, in.FileID, in.FolderID, in.RestoreTo)
	if err != nil {
		return nil, Entry{}, err
	}
	e := Entry{Name: md.Name, IsFolder: md.IsFolder}
	if md.IsFolder {
		e.ID = md.FolderID
	} else {
		e.ID = md.FileID
		e.Size = md.Size
		e.ContentType = md.ContentType
	}
	return nil, e, nil
}

// --- list_revisions / revert_revision ---

// ListRevisionsInput selects the file whose history to list.
type ListRevisionsInput struct {
	FileID int64 `json:"file_id" jsonschema:"pCloud file id whose version history to list"`
}

// RevisionInfo is one saved revision of a file.
type RevisionInfo struct {
	RevisionID int64  `json:"revision_id"`
	Size       int64  `json:"size"`
	Hash       string `json:"hash,omitempty"`
	Created    string `json:"created,omitempty"`
}

// ListRevisionsOutput is a file's saved revisions.
type ListRevisionsOutput struct {
	Revisions []RevisionInfo `json:"revisions"`
	Total     int            `json:"total"`
}

// ListRevisions lists a file's saved revisions (version history).
func (s *Server) ListRevisions(ctx context.Context, _ *mcp.CallToolRequest, in ListRevisionsInput) (*mcp.CallToolResult, ListRevisionsOutput, error) {
	revs, err := s.client.ListRevisions(ctx, in.FileID)
	if err != nil {
		return nil, ListRevisionsOutput{}, err
	}
	out := ListRevisionsOutput{Total: len(revs)}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, RevisionInfo{RevisionID: r.RevisionID, Size: r.Size, Hash: r.Hash, Created: r.Created})
	}
	return nil, out, nil
}

// RevertRevisionInput selects the file and the revision to revert to.
type RevertRevisionInput struct {
	FileID     int64 `json:"file_id" jsonschema:"pCloud file id to revert"`
	RevisionID int64 `json:"revision_id" jsonschema:"revision id to revert to (from pcloud_list_revisions)"`
}

// RevertRevision reverts a file to an earlier revision.
func (s *Server) RevertRevision(ctx context.Context, _ *mcp.CallToolRequest, in RevertRevisionInput) (*mcp.CallToolResult, Entry, error) {
	md, err := s.client.RevertRevision(ctx, in.FileID, in.RevisionID)
	if err != nil {
		return nil, Entry{}, err
	}
	return nil, Entry{Name: md.Name, ID: md.FileID, IsFolder: false, Size: md.Size, ContentType: md.ContentType}, nil
}
