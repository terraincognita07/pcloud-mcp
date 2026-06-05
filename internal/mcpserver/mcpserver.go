// Package mcpserver exposes the pCloud client as a set of MCP tools.
//
// Each tool is a thin, typed handler over the already-tested internal packages:
// pcloud (HTTP client), download (filesystem-bounded writes), and safepath (the
// traversal guard). Destructive and outward-facing tools are tagged with the
// matching MCP ToolAnnotations so a client can surface the risk to the user
// before the call is approved.
//
// Tools are methods on Server so they can be unit-tested directly with an
// httptest-backed pcloud.Client, without standing up an MCP transport. The
// handlers are grouped by domain across the tools_*.go files; this file holds
// the registration, the shared result shapes, and the small shared helpers.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
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

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_account_info",
		Description: "Return account information for the authenticated pCloud user: email (and whether it's verified), storage quota and used space in bytes, and premium status. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.AccountInfo)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_file_info",
		Description: "Return metadata for one pCloud file by file_id without downloading it: name, size, content type, created/modified time, and content hashes (sha256/sha1/md5, where the account's region provides them). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.FileInfo)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_links",
		Description: "List the account's existing public links (created by share_file / create_upload_link): each link's id, URL, the target's name, and download count. Use pcloud_delete_link to revoke one. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListLinks)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_trash",
		Description: "List items in pCloud's Trash (folder_id 0 = Trash root). Paged like list_folder (offset/limit). Each entry shows orig_parent_folder_id — where it lived before deletion. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListTrash)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_shares",
		Description: "List folder shares with other pCloud users: established shares and pending requests, each split into incoming (shared with you) and outgoing (you shared out), with the other party's email, folder, and permissions. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListShares)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_revisions",
		Description: "List the saved revisions (version history) of a file by file_id: revision id, size, hash, and creation time. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListRevisions)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_get_zip_link",
		Description: "Get a temporary URL to download a folder (and all its contents) as a single zip archive. Read-only; the zip is built by pCloud.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.GetZipLink)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_get_media_link",
		Description: "Get a temporary streaming URL for a video or audio file by file_id (set audio=true for audio). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.GetMediaLink)

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
		Name:        "pcloud_copy_file",
		Description: "Copy a pCloud file into another folder (to_folder_id 0 = root), optionally under new_name. The original is left in place; returns the new file.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.CopyFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_copy_folder",
		Description: "Copy a pCloud folder and all its contents into another folder (to_folder_id 0 = root), optionally under new_name. The original is left in place; returns the new folder.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.CopyFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_restore_from_trash",
		Description: "Restore a file or folder from pCloud's Trash by file_id OR folder_id (from pcloud_list_trash). Optionally restore_to a different folder; omit to put it back where it was.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.RestoreFromTrash)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_share_file",
		Description: "Create a public share link for a pCloud file. Anyone with the link can access the file, so confirm intent before sharing. Optional: expire_date, password, max_downloads. Returns link_id (revoke with pcloud_delete_link).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.ShareFile)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_share_folder",
		Description: "Create a public share link for an entire pCloud folder. Anyone with the link can browse and download the folder, so confirm intent. Optional: expire_date, password, max_downloads. Returns link_id (revoke with pcloud_delete_link).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.ShareFolder)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_link",
		Description: "Revoke a public link by link_id (from pcloud_list_links) so the URL stops working. This tightens access — the shared file/folder itself is untouched.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.DeleteLink)

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

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_list_upload_links",
		Description: "List the account's existing upload links (created by create_upload_link): each link's id, URL, target folder, comment, and number of files received. Use pcloud_delete_upload_link to close one. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, s.ListUploadLinks)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_delete_upload_link",
		Description: "Delete an upload link by upload_link_id (from pcloud_list_upload_links), closing that anonymous write path. Files already uploaded through it are untouched.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.DeleteUploadLink)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_share_folder_with_user",
		Description: "Share a pCloud folder with ANOTHER pCloud user by email, granting them access to your data. Read access is always granted; set can_create/can_modify/can_delete for write access. Outward-facing — confirm intent. Manage with pcloud_list_shares / pcloud_remove_share.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.ShareFolderWithUser)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_remove_share",
		Description: "Revoke a folder share (or pending request) by share_id (from pcloud_list_shares), removing that user's access.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.RemoveShare)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_revert_revision",
		Description: "Revert a file to an earlier revision by file_id + revision_id (from pcloud_list_revisions). Reversible: the current content becomes a new revision.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.RevertRevision)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "pcloud_upload_from_url",
		Description: "Have pCloud fetch a remote URL directly into a folder (folder_id 0 = root). The bytes go from the source straight to pCloud, never through this server — so it works in HTTP mode too. Outward-facing (fetches an external URL into your account) — confirm intent. Returns the new file(s).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, s.UploadFromURL)
}

// --- shared result shapes ---

// Entry is a compact view of one folder child (or a file produced by an
// operation that returns a single item).
type Entry struct {
	Name        string `json:"name"`
	ID          int64  `json:"id"` // folderid for folders, fileid for files
	IsFolder    bool   `json:"is_folder"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// DeleteResult acknowledges a delete/revoke that returns no payload.
type DeleteResult struct {
	Deleted bool `json:"deleted"`
}

// OKResult is a minimal success acknowledgement for tools with no payload.
type OKResult struct {
	OK bool `json:"ok"`
}

// LinkResult carries a single resolved URL.
type LinkResult struct {
	Link string `json:"link"`
}

// --- shared paging ---

// Listing limits keep a single response bounded so a large folder cannot
// overflow the client's context window. pCloud returns the whole folder in one
// call, so the page is sliced server-side.
const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

// paginate clamps offset/limit against total and returns the [start,end) window
// to slice, plus whether more remains and the offset to continue from. A
// non-positive limit defaults to defaultListLimit; it is capped at maxListLimit.
func paginate(total, offset, limit int) (start, end int, hasMore bool, nextOffset int) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	start = offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end = start + limit
	if end > total {
		end = total
	}
	if end < total {
		hasMore = true
		nextOffset = end
	}
	return start, end, hasMore, nextOffset
}
