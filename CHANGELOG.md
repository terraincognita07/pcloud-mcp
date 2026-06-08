# Changelog

All notable changes to this project are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); this project uses [SemVer](https://semver.org/).

## [0.4.0] — 2026-06-05

### Removed
- Dropped 12 tools that pCloud does not support over OAuth `access_token` auth, so they could never
  work in this OAuth-only server (every call returned `result 1000 "Log in required"`, or `2076 "no
  permissions"` for the user-share family). The token itself is valid — the remaining 21 tools work —
  but pCloud gates these specific methods behind the legacy `auth` token, a long-standing API
  limitation also seen by other OAuth clients (e.g. rclone's trash/cleanup). Shipping them as live
  tools was misleading. Removed: `pcloud_list_trash`, `pcloud_restore_from_trash` (trash_list /
  trash_restore), `pcloud_list_revisions`, `pcloud_revert_revision`, `pcloud_get_zip_link`,
  `pcloud_list_links` (listpublinks), `pcloud_create_upload_link`, `pcloud_list_upload_links`,
  `pcloud_delete_upload_link`, `pcloud_share_folder_with_user`, `pcloud_list_shares`,
  `pcloud_remove_share`. Public link/sharing that *does* work is unchanged: `pcloud_share_file`,
  `pcloud_share_folder` (create a link) and `pcloud_delete_link` (revoke it) remain.
- Tool surface: 33 → 21 (18 cloud-side over HTTP; 3 local-disk in stdio mode).

### Fixed
- Delete wording corrected across the server instruction, tool descriptions, and docs: deletes move to
  pCloud Trash (recoverable for a plan-dependent window), they do not erase permanently. Locked by
  `TestServerInstructions_DeleteWordingIsAccurate`.

## [0.3.0] — 2026-06-05

### Added
- `pcloud_share_folder_with_user` / `pcloud_list_shares` / `pcloud_remove_share` — share a folder with
  another pCloud user by email (read or read/write via can_create/can_modify/can_delete), list shares
  and pending requests (incoming/outgoing), and revoke access.
- `pcloud_list_revisions` / `pcloud_revert_revision` — list a file's version history and revert to an
  earlier revision (reversible — the current content becomes a new revision).
- `pcloud_get_zip_link` — temporary link to download a folder and its contents as one zip.
- `pcloud_upload_from_url` — have pCloud fetch a remote URL straight into a folder (bytes never pass
  through this server, so it works in HTTP mode).
- `pcloud_get_media_link` — temporary streaming URL for a video or audio file.
- `pcloud_share_folder` — create a public link to a whole folder (not just a file).
- `pcloud_list_upload_links` / `pcloud_delete_upload_link` — list and remove the account's upload links.
- `pcloud_list_links` / `pcloud_delete_link` — list the account's existing public links and revoke one
  by id (the shared file/folder itself is untouched). Gives a direct undo for `share_file`.
- `pcloud_list_trash` / `pcloud_restore_from_trash` — list items in pCloud's Trash (paged; each entry
  shows where it lived before deletion) and restore a file or folder, to its original spot or a chosen
  folder. No permanent-delete tool is exposed, so every deletion stays recoverable.
- `pcloud_account_info` — account email, storage quota and used space, premium status (read-only).
- `pcloud_file_info` — one file's metadata (size, content type, dates) and content hashes
  (sha256/sha1/md5, region-dependent) without downloading it (read-only).
- `pcloud_copy_file` / `pcloud_copy_folder` — copy a file or folder (with contents) into another
  folder, optionally under a new name; the original is left in place.
- `pcloud_read_file` — read a file by `file_id` and return its content inline: text as text, viewable
  images (JPEG/PNG/GIF/WebP) as an image. Oversized files (over `max_bytes`, default 5 MiB, max 10 MiB)
  and non-text/non-image binaries return a temporary download link instead, so a large file never
  overflows the context. Available in both stdio and HTTP modes.
- `pcloud_get_thumbnail` — fetch a small JPEG preview of an image or video by `file_id` and return it
  inline, so the model can see it. Renders to JPEG server-side (works for formats the model can't read
  directly, e.g. BMP), size-bounded (`WIDTHxHEIGHT`, default 256x256, max 1024x1024) and byte-capped.
  Available in both stdio and HTTP modes (it doesn't write local disk). Enables cheap visual
  scanning/identification of photos without pulling full-resolution files.

### Changed
- `pcloud_list_folder` now pages: optional `offset`/`limit` (default 200, max 1000) and returns
  `total`/`has_more`/`next_offset`, so a large folder no longer overflows the client's context.
  Backward-compatible — the defaults leave small-folder behavior unchanged.
- `pcloud_share_file` gains optional `expire_date` / `password` / `max_downloads` and now returns
  `link_id` (revoke via `pcloud_delete_link`). Backward-compatible — the new inputs are optional.

### Security
- Red-team follow-up: documented the expanded prompt-injection surface (`read_file`/`get_thumbnail`
  ingest file content inline, including in HTTP mode) and the outward-facing tools that grant external
  access (`share_*`, `share_folder_with_user`, `create_upload_link`, `upload_from_url`) in SECURITY.md
  "Known limitations". These stay *additive* (not `DestructiveHint` — that would be untruthful), but a
  new regression test (`TestIntegration_OutwardToolsNotHarmless`) locks them as non-`ReadOnlyHint` +
  `OpenWorldHint` so a host can never silently auto-run them as a harmless read.
- Narrowed the OAuth-only guard (`TestOAuthOnly_NoPasswordFlow`): removed the bare `"password"` token
  so pCloud's link-password option (`linkpassword`, used by `share_file`/`share_folder`) no longer
  false-positives. A real username/password login is still blocked — it cannot exist without
  `getauth`/`userauth`/`getdigest`/`passworddigest`, and the guard still trips on `username`. The
  non-vacuous guard test (a planted `getauth?username=…&password=…`) still fails the build.

## [0.2.1] — 2026-06-05

### Security
- Bump pinned build toolchain `go1.26.3 → go1.26.4` for two reachable stdlib advisories:
  GO-2026-5039 (`net/textproto`) and GO-2026-5037 (`crypto/x509`), both reached from the OAuth
  paths.
- Bump indirect dep `golang.org/x/sys` v0.41.0 → v0.44.0 (GO-2026-5024, not reachable). `govulncheck`
  is now fully clean — 0 findings, including required modules.

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

[0.4.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.4.0
[0.3.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.3.0
[0.2.1]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.2.1
[0.2.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.2.0
[0.1.0]: https://github.com/terraincognita07/pcloud-mcp/releases/tag/v0.1.0
