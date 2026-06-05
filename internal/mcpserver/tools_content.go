package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// GetThumbnailInput selects an image/video and an optional preview size.
type GetThumbnailInput struct {
	FileID int64  `json:"file_id" jsonschema:"pCloud file id of an image or video (from pcloud_list_folder)"`
	Size   string `json:"size,omitempty" jsonschema:"thumbnail size as WIDTHxHEIGHT, e.g. 256x256; default 256x256, each dimension 16..1024"`
}

// ThumbnailOutput is the structured side of a thumbnail (the JPEG itself is the
// tool result's image content).
type ThumbnailOutput struct {
	FileID int64  `json:"file_id"`
	Size   string `json:"size"`
	Bytes  int64  `json:"bytes"`
}

// GetThumbnail returns a small JPEG preview of a file inline.
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

// ReadFileInput selects a file and an optional inline byte cap.
type ReadFileInput struct {
	FileID   int64 `json:"file_id" jsonschema:"pCloud file id to read (from pcloud_list_folder)"`
	MaxBytes int   `json:"max_bytes,omitempty" jsonschema:"max bytes to pull inline; default 5MiB, hard max 10MiB. A larger file returns a temporary download link instead of content"`
}

// ReadFileOutput describes how the content came back: text/image inline, or a
// link for oversized/binary files.
type ReadFileOutput struct {
	FileID      int64  `json:"file_id"`
	Kind        string `json:"kind"` // "text", "image", or "link"
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	Link        string `json:"link,omitempty"` // set when kind == "link"
}

// ReadFile returns a file's content inline (text or image), falling back to a
// temporary download link for oversized or non-renderable files.
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
