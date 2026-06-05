# Changelog

All notable changes to this project are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); this project uses [SemVer](https://semver.org/).

## [0.2.1] — 2026-06-05

### Fixed
- `pcloud_list_folder`, `pcloud_save_text`, and other metadata-returning calls failed to decode pCloud
  responses whose `hash` exceeds `math.MaxInt64` (a real, full-range unsigned 64-bit value), aborting the
  whole response. `Metadata.Hash` is now `uint64`. Regression test `TestListFolder_LargeUnsignedHash`
  reproduces the original failure. `delete_*` were unaffected (their responses are decoded envelope-only).

### Changed
- `pcloud_delete_file` / `pcloud_delete_folder` descriptions corrected: pCloud routes deletes to its
  time-limited Trash (plan-dependent recovery window) rather than erasing immediately, so the prior
  "permanently delete … cannot be undone" wording overstated irreversibility. The tools remain flagged
  `DestructiveHint=true`; no behavior change.

## [0.2.0] — 2026-06-01

### Added
- `pcloud_save_text` — write text content straight into a new pCloud file, no local file needed.
- `pcloud_create_upload_link` — create a public, anonymous upload link to collect files into a folder
  (e.g. from a phone or another person). The `save_text` file name is validated through `safepath`.
- **HTTP transport** (`serve --http :addr`) for remote access from Claude.ai web/phone, alongside the
  existing stdio mode. Bearer-token authenticated (constant-time compare, fails closed without a token,
  `ReadHeaderTimeout` set). HTTP mode hides the local-filesystem tools (`download_*`, `upload_file`),
  which would otherwise write to the server's disk.
- `docker-compose.yml` (loopback bind, read-only, non-root, caps dropped) and
  [docs/SELF-HOSTING.md](docs/SELF-HOSTING.md) with nginx / Caddy / Traefik reverse-proxy examples.

### Security
- New `internal/httpserver` package isolates the network boundary: bearer auth, loopback bind, graceful
  shutdown. See SECURITY.md → "HTTP (remote) mode".

## [0.1.0] — 2026-06-01

First release. A hardened, ground-up Go reimplementation of an MCP server for pCloud.

### Added
- 10 MCP tools: list folder; download file/folder; upload file; create folder; move/rename file/folder;
  delete file/folder; share file.
- OAuth 2.0 authorization-code setup (`pcloud-mcp auth`) and stdio MCP server (`pcloud-mcp serve`).
- Single static binary; non-root distroless container; CI with build/vet/test plus
  `govulncheck`/`staticcheck`/`gosec`.

### Security
- **Path traversal closed (two layers).** Remote names are validated by `internal/safepath` after
  decoding (fails closed), and all file I/O runs through an `os.Root` scoped to the destination so the
  kernel refuses any escape, including a symlink planted mid-download (TOCTOU). A folder shared with the
  user named `..` cannot walk a download out of its directory.
- **Clean static analysis.** `gosec`, `staticcheck`, and `go vet` run clean; CI pins all tool versions
  and gates on `gofmt`, `go test -race`, and a non-root `docker build`.
- **Token handling.** Access token sent in POST body (not URL query), stored `0600` via atomic write,
  never printed; redacted in `String()`.
- **OAuth.** Loopback bind (`127.0.0.1`), constant-time 256-bit `state` (CSRF), callback race/DoS
  closed (bogus callbacks don't abort setup), malformed `locationid` no longer crashes.
- **Download URL.** `getfilelink` host/path validated to prevent upstream `host@evil` URL confusion.
- **Supply chain.** Build toolchain pinned to a release with current stdlib fixes; `govulncheck` clean.

[0.2.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.2.0
[0.1.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.1.0
