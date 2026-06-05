package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

// connect stands up a real MCP server with the pCloud tools registered, wires a
// real MCP client to it over the in-memory transport, and returns the client
// session. This exercises the whole stack — schema generation, transport,
// dispatch, typed argument unmarshaling — exactly as a host like Claude would.
func connect(t *testing.T, apiHandler http.HandlerFunc) *mcp.ClientSession {
	return connectMode(t, apiHandler, ModeLocal)
}

func connectMode(t *testing.T, apiHandler http.HandlerFunc, mode Mode) *mcp.ClientSession {
	t.Helper()

	api := httptest.NewServer(apiHandler)
	t.Cleanup(api.Close)
	u, _ := url.Parse(api.URL)
	hc := &http.Client{Transport: redirectTransport{base: u}}
	client := pcloud.New("tok", pcloud.RegionUS, pcloud.WithHTTPClient(hc))

	srv := mcp.NewServer(&mcp.Implementation{Name: "pcloud", Version: "test"}, nil)
	New(client).RegisterMode(srv, mode)

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
		Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// TestIntegration_AllToolsRegistered asserts the full, expected tool surface is
// advertised over the protocol — a guard against a tool silently dropping out of
// Register or being renamed.
func TestIntegration_AllToolsRegistered(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	want := []string{
		"pcloud_list_folder", "pcloud_get_thumbnail", "pcloud_read_file", "pcloud_account_info", "pcloud_file_info", "pcloud_list_links", "pcloud_list_trash", "pcloud_download_file", "pcloud_download_folder",
		"pcloud_upload_file", "pcloud_create_folder", "pcloud_delete_file",
		"pcloud_delete_folder", "pcloud_move_file", "pcloud_move_folder",
		"pcloud_copy_file", "pcloud_copy_folder", "pcloud_restore_from_trash", "pcloud_delete_link",
		"pcloud_share_file", "pcloud_save_text", "pcloud_create_upload_link",
	}
	if len(res.Tools) != len(want) {
		t.Errorf("tool count = %d; want %d", len(res.Tools), len(want))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("tool %q not advertised", name)
		}
	}
}

// TestIntegration_RemoteModeHidesLocalDiskTools verifies that the HTTP/remote
// tool set omits the three tools that read or write the server's local disk,
// while keeping the cloud-side tools. Exposing download_*/upload_file on a
// hosted server would touch the server's filesystem, not the user's.
func TestIntegration_RemoteModeHidesLocalDiskTools(t *testing.T) {
	cs := connectMode(t, func(w http.ResponseWriter, r *http.Request) {}, ModeRemote)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, hidden := range []string{"pcloud_download_file", "pcloud_download_folder", "pcloud_upload_file"} {
		if got[hidden] {
			t.Errorf("remote mode must NOT expose local-disk tool %q", hidden)
		}
	}
	for _, present := range []string{"pcloud_list_folder", "pcloud_share_file", "pcloud_save_text", "pcloud_create_upload_link", "pcloud_delete_file"} {
		if !got[present] {
			t.Errorf("remote mode must still expose cloud tool %q", present)
		}
	}
}

// TestIntegration_DeleteToolMarkedDestructive verifies the destructive-operation
// annotations actually reach the wire, so a host can warn the user before a
// permanent delete.
func TestIntegration_DeleteToolMarkedDestructive(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		switch tool.Name {
		case "pcloud_delete_file", "pcloud_delete_folder":
			if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
				t.Errorf("%s must carry DestructiveHint=true", tool.Name)
			}
		case "pcloud_list_folder":
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s must carry ReadOnlyHint=true", tool.Name)
			}
		}
	}
}

// TestIntegration_CallListFolder calls a tool the whole way through the protocol
// and checks the structured result, confirming argument marshaling and output
// schema round-trip end to end.
func TestIntegration_CallListFolder(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":0,"metadata":{"name":"root","folderid":0,"isfolder":true,"contents":[
			{"name":"a.txt","isfolder":false,"fileid":5,"size":3}
		]}}`)
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "pcloud_list_folder",
		Arguments: map[string]any{"folder_id": 0},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported error: %v", res.Content)
	}
	// StructuredContent carries the typed Out value.
	raw, _ := json.Marshal(res.StructuredContent)
	var out ListFolderOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Name != "a.txt" || out.Entries[0].ID != 5 {
		t.Errorf("structured result wrong: %+v", out)
	}
}

// TestIntegration_ToolErrorSurfaces confirms a handler error is delivered as a
// tool-level error result (IsError) rather than killing the session, so the LLM
// can see and react to it.
func TestIntegration_ToolErrorSurfaces(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"result":2005,"error":"Directory does not exist."}`)
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "pcloud_list_folder",
		Arguments: map[string]any{"folder_id": 999},
	})
	if err != nil {
		t.Fatalf("transport-level error (should be tool-level): %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for an upstream API failure")
	}
}
