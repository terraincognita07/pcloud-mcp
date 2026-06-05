package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

// --- share_folder_with_user ---

// ShareFolderWithUserInput grants another pCloud user access to a folder.
type ShareFolderWithUserInput struct {
	FolderID  int64  `json:"folder_id" jsonschema:"pCloud folder id to share"`
	Email     string `json:"email" jsonschema:"email of the pCloud user to share the folder with"`
	CanCreate bool   `json:"can_create,omitempty" jsonschema:"allow them to create files/subfolders"`
	CanModify bool   `json:"can_modify,omitempty" jsonschema:"allow them to modify files"`
	CanDelete bool   `json:"can_delete,omitempty" jsonschema:"allow them to delete files"`
	Name      string `json:"name,omitempty" jsonschema:"optional share name; defaults to the folder name"`
	Message   string `json:"message,omitempty" jsonschema:"optional message to the recipient"`
}

// ShareFolderWithUser shares a folder with another pCloud user by email.
func (s *Server) ShareFolderWithUser(ctx context.Context, _ *mcp.CallToolRequest, in ShareFolderWithUserInput) (*mcp.CallToolResult, OKResult, error) {
	if in.Email == "" {
		return nil, OKResult{}, fmt.Errorf("email is required")
	}
	perms := 0
	if in.CanCreate {
		perms |= pcloud.SharePermCreate
	}
	if in.CanModify {
		perms |= pcloud.SharePermModify
	}
	if in.CanDelete {
		perms |= pcloud.SharePermDelete
	}
	if err := s.client.ShareFolderWithUser(ctx, in.FolderID, in.Email, perms, in.Name, in.Message); err != nil {
		return nil, OKResult{}, err
	}
	return nil, OKResult{OK: true}, nil
}

// --- list_shares ---

// ShareEntry is one folder share (or pending request) with another user.
type ShareEntry struct {
	ShareID        int64  `json:"share_id,omitempty"`
	ShareRequestID int64  `json:"share_request_id,omitempty"`
	FolderID       int64  `json:"folder_id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	Permissions    string `json:"permissions"`
}

// ListSharesInput takes no parameters.
type ListSharesInput struct{}

// ListSharesOutput groups shares and requests by direction.
type ListSharesOutput struct {
	IncomingShares   []ShareEntry `json:"incoming_shares"`
	OutgoingShares   []ShareEntry `json:"outgoing_shares"`
	IncomingRequests []ShareEntry `json:"incoming_requests"`
	OutgoingRequests []ShareEntry `json:"outgoing_requests"`
}

func toShareEntry(sh pcloud.Share) ShareEntry {
	email := sh.ToMail
	if email == "" {
		email = sh.FromMail
	}
	var perms []string
	if sh.CanRead {
		perms = append(perms, "read")
	}
	if sh.CanCreate {
		perms = append(perms, "create")
	}
	if sh.CanModify {
		perms = append(perms, "modify")
	}
	if sh.CanDelete {
		perms = append(perms, "delete")
	}
	return ShareEntry{
		ShareID:        sh.ShareID,
		ShareRequestID: sh.ShareRequestID,
		FolderID:       sh.FolderID,
		Name:           sh.ShareName,
		Email:          email,
		Permissions:    strings.Join(perms, ","),
	}
}

func mapShares(in []pcloud.Share) []ShareEntry {
	out := make([]ShareEntry, 0, len(in))
	for _, sh := range in {
		out = append(out, toShareEntry(sh))
	}
	return out
}

// ListShares lists folder shares with other pCloud users.
func (s *Server) ListShares(ctx context.Context, _ *mcp.CallToolRequest, _ ListSharesInput) (*mcp.CallToolResult, ListSharesOutput, error) {
	sl, err := s.client.ListShares(ctx)
	if err != nil {
		return nil, ListSharesOutput{}, err
	}
	return nil, ListSharesOutput{
		IncomingShares:   mapShares(sl.Shares.Incoming),
		OutgoingShares:   mapShares(sl.Shares.Outgoing),
		IncomingRequests: mapShares(sl.Requests.Incoming),
		OutgoingRequests: mapShares(sl.Requests.Outgoing),
	}, nil
}

// --- remove_share ---

// RemoveShareInput selects the share to revoke.
type RemoveShareInput struct {
	ShareID int64 `json:"share_id" jsonschema:"share id to revoke (from pcloud_list_shares)"`
}

// RemoveShare revokes a folder share (or pending request) with a user.
func (s *Server) RemoveShare(ctx context.Context, _ *mcp.CallToolRequest, in RemoveShareInput) (*mcp.CallToolResult, DeleteResult, error) {
	if err := s.client.RemoveShare(ctx, in.ShareID); err != nil {
		return nil, DeleteResult{}, err
	}
	return nil, DeleteResult{Deleted: true}, nil
}
