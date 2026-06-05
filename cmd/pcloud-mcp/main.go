// Command pcloud-mcp is a hardened Model Context Protocol server for pCloud.
//
// Usage:
//
//	pcloud-mcp auth              # one-time OAuth setup; saves credentials 0600
//	pcloud-mcp serve            # run over stdio (local, for Claude Desktop)
//	pcloud-mcp serve --http :8080   # run over authenticated HTTP (remote)
//
// The OAuth client id and secret are read from the environment for `auth`:
//
//	PCLOUD_CLIENT_ID, PCLOUD_CLIENT_SECRET
//
// HTTP mode additionally requires a bearer token in PCLOUD_MCP_TOKEN; requests
// must send "Authorization: Bearer <that token>". HTTP mode also hides the
// local-filesystem tools (download_*, upload_file), which would otherwise write
// to the server's disk rather than the user's.
//
// All diagnostics go to stderr; stdout is reserved for the MCP stdio protocol.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/config"
	"github.com/terraincognita07/pcloud-mcp/internal/httpserver"
	"github.com/terraincognita07/pcloud-mcp/internal/mcpserver"
	"github.com/terraincognita07/pcloud-mcp/internal/oauth"
	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

const version = "0.4.0"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error
	switch cmd {
	case "auth":
		err = runAuth()
	case "serve":
		err = runServe(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	case "-v", "--version", "version":
		fmt.Fprintln(os.Stderr, "pcloud-mcp "+version)
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pcloud-mcp `+version+` — hardened MCP server for pCloud

Usage:
  pcloud-mcp auth                 One-time OAuth setup (needs PCLOUD_CLIENT_ID / PCLOUD_CLIENT_SECRET)
  pcloud-mcp serve                Run over stdio (local; for Claude Desktop on this machine)
  pcloud-mcp serve --http :8080   Run over authenticated HTTP (remote; needs PCLOUD_MCP_TOKEN)

Credentials are stored with owner-only permissions under your user config dir.
In HTTP mode, clients must send "Authorization: Bearer $PCLOUD_MCP_TOKEN", and
the local-filesystem tools (download/upload) are hidden.
`)
}

// runAuth performs the OAuth flow and persists the credentials.
func runAuth() error {
	clientID := os.Getenv("PCLOUD_CLIENT_ID")
	clientSecret := os.Getenv("PCLOUD_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("set PCLOUD_CLIENT_ID and PCLOUD_CLIENT_SECRET (create an app at https://docs.pcloud.com/my_apps/)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	creds, err := oauth.Run(ctx, oauth.Config{ClientID: clientID, ClientSecret: clientSecret})
	if err != nil {
		return err
	}

	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if err := config.Save(path, creds); err != nil {
		return err
	}
	// Never print the token. Report only where it was stored and for whom.
	fmt.Fprintf(os.Stderr, "Authorized. Credentials saved to %s (region %d, uid %d).\n", path, creds.Region, creds.UID)
	fmt.Fprintln(os.Stderr, "You can now run: pcloud-mcp serve")
	return nil
}

// runServe loads credentials and serves the MCP tools, over stdio by default or
// over authenticated HTTP when --http is given.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	httpAddr := fs.String("http", "", "serve over authenticated HTTP on this address (e.g. :8080); requires PCLOUD_MCP_TOKEN")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := loadClient()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *httpAddr != "" {
		return serveHTTP(ctx, *httpAddr, client, logger)
	}
	return serveStdio(ctx, client, logger)
}

// loadClient reads stored credentials and builds a pCloud client.
func loadClient() (*pcloud.Client, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	creds, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("%w (run `pcloud-mcp auth` first)", err)
	}
	return pcloud.New(creds.AccessToken, pcloud.Region(creds.Region)), nil
}

// serverInstructions is the MCP server-level guidance shown to hosts. The delete
// wording must match reality: deletefile/deletefolderrecursive move items to
// pCloud Trash (recoverable for a plan-dependent window), they do NOT erase
// permanently — claiming otherwise misleads a host into over-warning the user.
// Guarded by TestServerInstructions_DeleteWordingIsAccurate.
const serverInstructions = "Tools for a pCloud account. List, organize, delete, and share files. " +
	"Deleting moves items to pCloud Trash, recoverable for a limited, plan-dependent " +
	"period before permanent purge; do not rely on Trash as a backup."

// newMCPServer builds the MCP server with the tool set for the given mode.
func newMCPServer(client *pcloud.Client, mode mcpserver.Mode, logger *slog.Logger) *mcp.Server {
	impl := &mcp.Implementation{Name: "pcloud", Title: "pCloud", Version: version}
	srv := mcp.NewServer(impl, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: serverInstructions,
	})
	mcpserver.New(client).RegisterMode(srv, mode)
	return srv
}

// serveStdio runs the full (local) tool set over stdio.
func serveStdio(ctx context.Context, client *pcloud.Client, logger *slog.Logger) error {
	srv := newMCPServer(client, mcpserver.ModeLocal, logger)
	logger.Info("pcloud-mcp serving over stdio", "version", version)
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// serveHTTP runs the remote tool set over bearer-authenticated HTTP. The token
// comes from PCLOUD_MCP_TOKEN; httpserver.Serve refuses to start without it.
func serveHTTP(ctx context.Context, addr string, client *pcloud.Client, logger *slog.Logger) error {
	token := os.Getenv("PCLOUD_MCP_TOKEN")
	if token == "" {
		return fmt.Errorf("HTTP mode requires PCLOUD_MCP_TOKEN (a long random secret clients send as a bearer token)")
	}
	srv := newMCPServer(client, mcpserver.ModeRemote, logger)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		nil,
	)
	return httpserver.Serve(ctx, addr, token, handler, logger)
}
