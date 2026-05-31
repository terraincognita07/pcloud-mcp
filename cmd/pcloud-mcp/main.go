// Command pcloud-mcp is a hardened Model Context Protocol server for pCloud.
//
// Usage:
//
//	pcloud-mcp auth     # one-time OAuth setup; saves credentials to a 0600 file
//	pcloud-mcp serve    # run the MCP server over stdio (default)
//
// The OAuth client id and secret are read from the environment:
//
//	PCLOUD_CLIENT_ID, PCLOUD_CLIENT_SECRET
//
// All diagnostics go to stderr; stdout is reserved for the MCP protocol.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terraincognita07/pcloud-mcp/internal/config"
	"github.com/terraincognita07/pcloud-mcp/internal/mcpserver"
	"github.com/terraincognita07/pcloud-mcp/internal/oauth"
	"github.com/terraincognita07/pcloud-mcp/internal/pcloud"
)

const version = "0.1.0"

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
		err = runServe()
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
  pcloud-mcp auth     One-time OAuth setup (needs PCLOUD_CLIENT_ID / PCLOUD_CLIENT_SECRET)
  pcloud-mcp serve    Run the MCP server over stdio (default)

Credentials are stored with owner-only permissions under your user config dir.
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

// runServe loads credentials and serves the MCP tools over stdio.
func runServe() error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	creds, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("%w (run `pcloud-mcp auth` first)", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client := pcloud.New(creds.AccessToken, pcloud.Region(creds.Region))

	impl := &mcp.Implementation{Name: "pcloud", Title: "pCloud", Version: version}
	srv := mcp.NewServer(impl, &mcp.ServerOptions{
		Logger: logger,
		Instructions: "Tools for a pCloud account. List, download, upload, organize, " +
			"delete, and share files. Local paths are restricted so remote names " +
			"cannot escape the destination directory. Delete operations are " +
			"permanent and cannot be undone.",
	})
	mcpserver.New(client).Register(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger.Info("pcloud-mcp serving over stdio", "version", version)
	return srv.Run(ctx, &mcp.StdioTransport{})
}
