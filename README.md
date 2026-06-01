# pcloud-mcp

A hardened [Model Context Protocol](https://modelcontextprotocol.io) server for [pCloud](https://www.pcloud.com), written in Go.

[![CI](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/terraincognita07/pcloud-mcp)](https://goreportcard.com/report/github.com/terraincognita07/pcloud-mcp)
[![Go Reference](https://pkg.go.dev/badge/github.com/terraincognita07/pcloud-mcp.svg)](https://pkg.go.dev/github.com/terraincognita07/pcloud-mcp)
[![Release](https://img.shields.io/github/v/release/terraincognita07/pcloud-mcp?display_name=tag)](https://github.com/terraincognita07/pcloud-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker)](https://github.com/terraincognita07/pcloud-mcp/blob/main/Dockerfile)

Give [Claude](https://claude.ai) (or any MCP host) access to your [pCloud](https://www.pcloud.com)
account — list, download, upload, organize, and share files in plain language. A single static binary
with no runtime dependencies, built so that an agent holding both a full-access cloud token and local
filesystem access cannot be walked out of bounds.

```
   Claude (MCP host)
        │  stdio (MCP)
        ▼
   pcloud-mcp ──OAuth token (POST body, never URL)──→ pCloud API (HTTPS)
        │
        │  every remote name → internal/safepath   (validate, fail closed)
        │  every local write → os.Root              (kernel-enforced containment)
        ▼
   your local disk — writes cannot escape the chosen directory
```

## Features

- **10 tools** — list folder; download file/folder; upload file; create folder; move/rename
  file/folder; delete file/folder; share link. Host config below.
- **Path-traversal–proof downloads** — pCloud folder names are attacker-influenced (a shared folder
  may be named `..`), so every remote name is validated *and* every write goes through an `os.Root`
  scoped to your destination. The kernel refuses any escape, even via a symlink planted mid-download.
- **OAuth 2.0 only** — loopback callback bound to `127.0.0.1`, CSRF `state` compared in constant time,
  token sent in the POST body (never the URL), stored `0600`, never printed. No password flow.
- **Destructive operations are flagged** — `delete_file` / `delete_folder` carry the MCP
  `DestructiveHint` so your host can warn you before a permanent, recursive delete.
- **Clean supply chain** — `govulncheck`, `staticcheck`, and `gosec` run clean and gate CI; the build
  toolchain is pinned. Zero third-party runtime dependencies beyond the official MCP SDK.

## Why this exists

This is a ground-up Go reimplementation built after a line-by-line audit of the existing Python
`pcloud-mcp-server` turned up a **critical path-traversal vulnerability**, plus OAuth and
token-handling weaknesses.

An MCP server for cloud storage is unusually sensitive: the host LLM is handed both a full-access cloud
token and local filesystem access, and **file/folder names returned by the pCloud API are
attacker-influenced** — a folder shared with the victim can legitimately be named `..`. A naive client
walks that name straight onto the local path and overwrites arbitrary files
(`~/.ssh/authorized_keys`, `~/.bashrc`, cron entries). Here, untrusted remote metadata is treated as a
first-class threat. The full hardening table and the specific attack each control closes are in
[SECURITY.md](SECURITY.md).

### How it differs from the existing server

| | Language | Path containment | Auth | Distribution |
|---|---|---|---|---|
| [`abiheiri/pcloud-mcp-server`](https://github.com/abiheiri/pcloud-mcp-server) | Python | None — vulnerable to `..` traversal | password-in-URL or OAuth | `uv` / Python env |
| **`pcloud-mcp`** | **Go** | **`safepath` + `os.Root`, fails closed** | **OAuth only** | **single static binary / distroless image** |

## Quick start

### Install

```sh
go install github.com/terraincognita07/pcloud-mcp/cmd/pcloud-mcp@latest
```

Or build from source:

```sh
git clone https://github.com/terraincognita07/pcloud-mcp.git
cd pcloud-mcp
go build -o pcloud-mcp ./cmd/pcloud-mcp
```

Or run the container (non-root, distroless):

```sh
docker build -t pcloud-mcp .
```

### 1. Create a pCloud app

Register an app at <https://docs.pcloud.com/my_apps/> and set its redirect URI to
`http://127.0.0.1:53682/callback`. Note the **client id** and **client secret**.

### 2. Authorize once

Credentials are saved to your user config dir with `0600` permissions; the token is never printed.

```sh
export PCLOUD_CLIENT_ID=xxxxxxxx
export PCLOUD_CLIENT_SECRET=yyyyyyyy
pcloud-mcp auth
```

PowerShell:

```powershell
$env:PCLOUD_CLIENT_ID = "xxxxxxxx"
$env:PCLOUD_CLIENT_SECRET = "yyyyyyyy"
pcloud-mcp auth
```

### 3. Add it to your MCP host

For Claude Desktop (`claude_desktop_config.json`):

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
|---|---|---|
| `pcloud_list_folder` | read-only | List a folder's contents (`folder_id` 0 = root). |
| `pcloud_download_file` | additive | Download one file to a local directory. |
| `pcloud_download_folder` | additive | Mirror a folder tree locally (traversal-checked). |
| `pcloud_upload_file` | additive | Upload a local file into a folder. |
| `pcloud_create_folder` | additive | Create a folder. |
| `pcloud_move_file` | additive | Rename and/or move a file. |
| `pcloud_move_folder` | additive | Rename and/or move a folder. |
| `pcloud_share_file` | additive | Create a public share link. |
| `pcloud_delete_file` | **destructive** | Permanently delete a file. |
| `pcloud_delete_folder` | **destructive** | Permanently delete a folder and all its contents. |

## Security

Hardening details, the specific attack each control closes, and the known (by-design) limitations of
the MCP trust model live in [SECURITY.md](SECURITY.md). The minimum audit gate, also enforced in CI:

```sh
go test ./... && go vet ./... && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Found a vulnerability? Please report it privately — see [SECURITY.md](SECURITY.md).

## Development

```sh
go test ./...        # all tests, incl. a real in-memory MCP integration test
go vet ./...
```

The code is layered one-directionally — `cmd → mcpserver → {download, oauth, config, pcloud} →
safepath` — with the security boundary isolated in `internal/safepath`. `safepath` and `pcloud` know
nothing about the filesystem layout or MCP, so the trust boundary stays in one auditable place.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

- [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk) — the official SDK,
  maintained in collaboration with Google.
- [pCloud API](https://docs.pcloud.com/) — the storage backend this server wraps.
