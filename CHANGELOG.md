# Changelog

All notable changes to this project are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); this project uses [SemVer](https://semver.org/).

## [0.1.0] — 2026-06-01

First release. A hardened, ground-up Go reimplementation of an MCP server for pCloud.

### Added
- 10 MCP tools: list folder; download file/folder; upload file; create folder; move/rename file/folder;
  delete file/folder; share file.
- OAuth 2.0 authorization-code setup (`pcloud-mcp auth`) and stdio MCP server (`pcloud-mcp serve`).
- Single static binary; non-root distroless container; CI with build/vet/test plus
  `govulncheck`/`staticcheck`/`gosec`.

### Security
- **Path traversal closed.** All local writes are contained to the chosen directory via
  `internal/safepath`; remote names are validated after decoding and the joined path re-checked.
  Reproduces and blocks the `..`-named-shared-folder attack present in the Python reference server.
- **Token handling.** Access token sent in POST body (not URL query), stored `0600` via atomic write,
  never printed; redacted in `String()`.
- **OAuth.** Loopback bind (`127.0.0.1`), constant-time 256-bit `state` (CSRF), callback race/DoS
  closed (bogus callbacks don't abort setup), malformed `locationid` no longer crashes.
- **Download URL.** `getfilelink` host/path validated to prevent upstream `host@evil` URL confusion.
- **Supply chain.** Build toolchain pinned to a release with current stdlib fixes; `govulncheck` clean.

[0.1.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.1.0
