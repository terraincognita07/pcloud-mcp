# pcloud-mcp

A **hardened** [Model Context Protocol](https://modelcontextprotocol.io) server for
[pCloud](https://www.pcloud.com), written in Go. Single static binary, no runtime dependencies.
Lets an MCP host like [Claude](https://claude.ai) list, download, upload, organize, and share files in
your pCloud account — with the filesystem and OAuth boundaries that a cloud-storage agent actually
needs.

> **Why "hardened"?** This is a ground-up Go reimplementation built after finding a critical
> **path-traversal** vulnerability (plus OAuth and token-handling weaknesses) in the existing Python
> `pcloud-mcp-server`. Folder names returned by the pCloud API are attacker-influenced — a shared
> folder can be named `..` — so a naive client can be walked out of its download directory and made to
> overwrite arbitrary files. Here, untrusted remote metadata is a first-class threat. See
> [SECURITY.md](SECURITY.md).

[![CI](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/terraincognita07/pcloud-mcp.svg)](https://pkg.go.dev/github.com/terraincognita07/pcloud-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/terraincognita07/pcloud-mcp)](https://goreportcard.com/report/github.com/terraincognita07/pcloud-mcp)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Features

- **10 tools** — list, download file/folder, upload, create folder, move/rename file/folder, delete
  file/folder, share link.
- **Path-traversal–proof downloads** — every remote name is validated and every local write is
  contained to your chosen directory. Fails closed.
- **OAuth 2.0 only** — loopback callback bound to `127.0.0.1`, CSRF `state`, token stored `0600`,
  never printed. No password flow.
- **Destructive operations annotated** — `delete_*` carry the MCP `DestructiveHint` so your host can
  warn you before a permanent delete.
- **Clean supply chain** — `govulncheck`-clean, toolchain pinned, CI-gated. Zero third-party runtime
  deps beyond the official MCP SDK.

## Install

```sh
go install github.com/terraincognita07/pcloud-mcp/cmd/pcloud-mcp@latest
```

Or build from source:

```sh
git clone https://github.com/terraincognita07/pcloud-mcp
cd pcloud-mcp
go build -o pcloud-mcp ./cmd/pcloud-mcp
```

Or run the container (non-root):

```sh
docker build -t pcloud-mcp .
```

## Setup

1. Create a pCloud app at <https://docs.pcloud.com/my_apps/> and set its redirect URI to
   `http://127.0.0.1:53682/callback`. Note the **client id** and **client secret**.

2. Authorize once. Credentials are saved to your user config dir with `0600` permissions; the token is
   never printed.

   ```sh
   export PCLOUD_CLIENT_ID=xxxxxxxx
   export PCLOUD_CLIENT_SECRET=yyyyyyyy
   pcloud-mcp auth
   ```

   (Windows PowerShell: `$env:PCLOUD_CLIENT_ID="…"` etc.)

3. Add it to your MCP host. For Claude Desktop (`claude_desktop_config.json`):

   ```json
   {
     "mcpServers": {
       "pcloud": {
         "command": "pcloud-mcp",
         "args": ["serve"]
       }
     }
   }
   ```

   That's it — `serve` reads the saved credentials and speaks MCP over stdio.

## Tools

| Tool | Kind | Description |
|------|------|-------------|
| `pcloud_list_folder` | read-only | List a folder's contents (`folder_id` 0 = root). |
| `pcloud_download_file` | additive | Download one file to a local directory. |
| `pcloud_download_folder` | additive | Mirror a folder tree locally (traversal-checked). |
| `pcloud_upload_file` | additive | Upload a local file into a folder. |
| `pcloud_create_folder` | additive | Create a folder. |
| `pcloud_move_file` | additive | Rename and/or move a file. |
| `pcloud_move_folder` | additive | Rename and/or move a folder. |
| `pcloud_share_file` | additive | Create a public share link. |
| `pcloud_delete_file` | **destructive** | Permanently delete a file. |
| `pcloud_delete_folder` | **destructive** | Permanently delete a folder and all contents. |

## Security

Hardening details, the specific attack each control closes, and known (by-design) limitations are in
[SECURITY.md](SECURITY.md). Minimum audit gate:

```sh
go test ./... && go vet ./... && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Development

```sh
go test ./...        # all tests, incl. a real in-memory MCP integration test
go vet ./...
```

Architecture is layered one-directionally — `cmd → mcpserver → {download, oauth, config, pcloud} →
safepath` — with the security boundary isolated in `internal/safepath`.

## License

[MIT](LICENSE).
