# pcloud-mcp

A hardened [Model Context Protocol](https://modelcontextprotocol.io) server for [pCloud](https://www.pcloud.com), written in Go.

[![CI](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/terraincognita07/pcloud-mcp/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/terraincognita07/pcloud-mcp/branch/main/graph/badge.svg)](https://codecov.io/gh/terraincognita07/pcloud-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/terraincognita07/pcloud-mcp)](https://goreportcard.com/report/github.com/terraincognita07/pcloud-mcp)
[![Go Reference](https://pkg.go.dev/badge/github.com/terraincognita07/pcloud-mcp.svg)](https://pkg.go.dev/github.com/terraincognita07/pcloud-mcp)
[![Release](https://img.shields.io/github/v/release/terraincognita07/pcloud-mcp?display_name=tag)](https://github.com/terraincognita07/pcloud-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker)](https://github.com/terraincognita07/pcloud-mcp/blob/main/Dockerfile)

Give [Claude](https://claude.ai) (or any MCP host) access to your [pCloud](https://www.pcloud.com)
account — list, download, upload, organize, and share files in plain language. A single static binary
with no runtime dependencies, designed so that an agent holding both a full-access cloud token and
local filesystem access stays safely within the bounds you set.

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

## What you can do

Once it's connected, you talk to your pCloud in plain language. For example:

- *"What's in my pCloud root? Open the Photos folder."* — browse and search your files.
- *"Download the Invoices folder to ~/Documents."* — pull a file or a whole tree to your machine (local mode).
- *"Upload report.pdf to my Work folder."* — push a local file up (local mode).
- *"Save this summary as notes.md in my Journal folder."* — write text Claude generated straight into a file, no local copy needed.
- *"Make a new folder 2026, move last year's files into it, rename the old one to Archive."* — organize without the web UI.
- *"Give me a link to share invoice.pdf."* — get a public download link (works from your phone too).
- *"Delete the temp folder."* — remove files/folders (your host asks you to confirm — these are flagged destructive).

It works two ways: **locally** through Claude Desktop (files land on your computer), or as a **remote server** so Claude.ai on the web or your phone can reach your pCloud from anywhere — see [Remote access](#remote-access-claudeai-web--phone).

## Features

- **21 tools** covering the account: browse, image/video thumbnails, read file content, account/file
  info, download/upload (incl. fetch-from-URL), save text, create/copy/move/rename/delete folders and
  files, public links for files and folders (with expiry/password/limits, revoke), and streaming
  links — see [What you can do](#what-you-can-do) above and the [Tools](#tools) table below.
- **Path-traversal–proof downloads** — pCloud folder names are attacker-influenced (a shared folder
  may be named `..`), so every remote name is validated *and* every write goes through an `os.Root`
  scoped to your destination. The kernel refuses any escape, even via a symlink planted mid-download.
- **OAuth 2.0 only** — loopback callback bound to `127.0.0.1`, CSRF `state` compared in constant time,
  token sent in the POST body (never the URL), stored `0600`, never printed. No password flow.
- **Destructive operations are flagged** — `delete_file` / `delete_folder` carry the MCP
  `DestructiveHint` so your host can warn you before a recursive delete. pCloud routes deletes
  to its time-limited Trash (plan-dependent recovery window) rather than erasing immediately —
  treat it as destructive, not as a backup.
- **Clean supply chain** — `govulncheck`, `staticcheck`, and `gosec` run clean and gate CI; the build
  toolchain is pinned. Zero third-party runtime dependencies beyond the official MCP SDK.

## Why this exists

A cloud-storage MCP server sits in an unusually sensitive spot: the host LLM holds a full-access cloud
token **and** can write to your local disk, and the file/folder names it acts on come from the cloud,
not from you — so they are untrusted input. A folder shared with you can legitimately be named `..`.
That makes "validate everything that comes back from the API, and contain every write" a design
requirement, not a nice-to-have.

This server is built around that from the ground up: every remote name is validated before it is used,
and every write is contained by the OS kernel, so a name can never move a write outside the directory
you chose. The full hardening table — each control and the class of attack it closes — is in
[SECURITY.md](SECURITY.md).

### Design priorities

| | This server |
|---|---|
| **Path containment** | `internal/safepath` validation + `os.Root` kernel-level scoping; fails closed |
| **Auth** | OAuth 2.0 only — token in the POST body, never a URL |
| **Transport** | stdio (local) and bearer-authenticated HTTP (remote) |
| **Distribution** | single static binary / non-root distroless image |

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

> [!NOTE]
> pCloud has currently paused self-service registration of **new** OAuth apps. If the
> "create application" option is unavailable, that's a pCloud-side restriction, not a
> limitation of pcloud-mcp. Already-registered apps keep working — reuse credentials
> from an app you created earlier, or ask pCloud support to provision one (redirect URI
> `http://127.0.0.1:53682/callback`). The server is OAuth-only by design and needs a
> client id / client secret to start; there is no password fallback.

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

## Remote access (Claude.ai web / phone)

`serve --http :8080` runs an **authenticated HTTP** server so Claude.ai in a
browser or on your phone can reach your pCloud from anywhere. It requires a
bearer token and a reverse proxy for HTTPS, and it **hides the local-filesystem
tools** (download/upload would write to the server's disk, not yours). Cloud-side
tools — browse, organize, **share link**, **save text**, **fetch from URL** — stay
available.

```sh
export PCLOUD_MCP_TOKEN=$(openssl rand -hex 32)
pcloud-mcp serve --http :8080      # bind behind a TLS reverse proxy
```

Full deployment (docker-compose + nginx / Caddy / Traefik + connecting Claude.ai)
is in [docs/SELF-HOSTING.md](docs/SELF-HOSTING.md).

## Tools

| Tool | Kind | Description |
|---|---|---|
| `pcloud_list_folder` | read-only | List a folder's contents (`folder_id` 0 = root); paged (`offset`/`limit`) so large folders don't overflow the context. |
| `pcloud_get_thumbnail` | read-only | Return a small JPEG preview of an image/video inline (works for BMP etc.) — for cheap visual scanning/identification. |
| `pcloud_read_file` | read-only | Return a file's content inline — text as text, viewable images as image; oversized/binary files return a temporary link. |
| `pcloud_account_info` | read-only | Account email, storage quota and used space, premium status. |
| `pcloud_file_info` | read-only | One file's metadata (size, type, dates) and content hashes, without downloading. |
| `pcloud_get_media_link` | read-only | Temporary streaming link for a video/audio file. |
| `pcloud_download_file` | additive | Download one file to a local directory. |
| `pcloud_download_folder` | additive | Mirror a folder tree locally (traversal-checked). |
| `pcloud_upload_file` | additive | Upload a local file into a folder. |
| `pcloud_create_folder` | additive | Create a folder. |
| `pcloud_move_file` | additive | Rename and/or move a file. |
| `pcloud_move_folder` | additive | Rename and/or move a folder. |
| `pcloud_copy_file` | additive | Copy a file into another folder (original left in place). |
| `pcloud_copy_folder` | additive | Copy a folder and its contents into another folder (original left in place). |
| `pcloud_upload_from_url` | additive | Have pCloud fetch a remote URL straight into a folder (works in HTTP mode). |
| `pcloud_share_file` | additive | Create a public share link to a file (optional expiry/password/max-downloads; returns link_id). |
| `pcloud_share_folder` | additive | Create a public share link to a whole folder (same options as share_file). |
| `pcloud_save_text` | additive | Write text straight into a new pCloud file — no local file needed. |
| `pcloud_delete_link` | additive | Revoke a public (download) link by id (the shared file/folder is untouched). |
| `pcloud_delete_file` | **destructive** | Delete a file (moved to pCloud's time-limited Trash). |
| `pcloud_delete_folder` | **destructive** | Delete a folder and all its contents recursively (moved to pCloud's time-limited Trash). |

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

The code is layered one-directionally — `cmd → mcpserver → {download, pcloud, safepath}`, with
`oauth`/`config` wired directly in `cmd` (not imported by `mcpserver`) and `cmd → httpserver`
wrapping the MCP handler at the network boundary for HTTP mode —
and the security boundary isolated in `internal/safepath`. `safepath` and `pcloud` know nothing about
the filesystem layout or MCP, so the trust boundary stays in one auditable place.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

- [Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk) — the official SDK,
  maintained in collaboration with Google.
- [pCloud API](https://docs.pcloud.com/) — the storage backend this server wraps.
