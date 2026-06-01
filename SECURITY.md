# Security Policy

## Reporting a vulnerability

Please report security issues privately via GitHub's
[private vulnerability reporting](https://github.com/terraincognita07/pcloud-mcp/security/advisories/new)
rather than a public issue. I aim to acknowledge within a few days. There is no bounty — this is a
personal open-source project — but credit is given in the advisory unless you prefer otherwise.

## Why this exists

An MCP server for cloud storage is unusually sensitive: the host LLM is handed both a full-access cloud
token and local filesystem access, and **file/folder names returned by the pCloud API are
attacker-influenced** — a folder shared with the user may legitimately be named `..`. The hardening
below is the point of the project, not an afterthought.

## Hardening (with the attack each closes)

| Area | Guarantee | Attack closed |
|------|-----------|---------------|
| Path containment | Two independent layers: `internal/safepath` validates every remote name (no `..`, separators, NUL, reserved names) **after** decoding and fails closed, and all file I/O runs through an `os.Root` scoped to the destination so the **kernel** refuses any escape — including a symlink planted mid-download (TOCTOU). | Path traversal via a shared folder named `..` → arbitrary file overwrite (`~/.ssh/authorized_keys`, `~/.bashrc`). |
| Token in transit | Access token sent in the POST body, never the URL query. | Token leakage into server/proxy access logs and browser history. |
| Token at rest | Credentials file is `0600`, written atomically (temp + rename). | Local disclosure / half-written file races. |
| Token in output | Token never printed to stdout; redacted in `String()` methods. | Leakage via terminal scrollback, screenshots, shell history. |
| OAuth bind | Callback server binds `127.0.0.1`, not `0.0.0.0`. | Other hosts on the LAN reaching/racing the callback. |
| OAuth state | Random 256-bit `state`, constant-time compared; required on callback. | OAuth CSRF (RFC 6749 §10.12) — binding the attacker's account. |
| OAuth callback race | A callback without the correct state is rejected over HTTP but does **not** abort setup; only the genuine state completes the flow. | Local process racing the browser to repeatedly kill setup (local DoS). |
| OAuth robustness | Malformed `locationid` falls back to US instead of crashing the handler. | Handler crash on a non-numeric `locationid`. |
| Download URL | `getfilelink` host+path validated structurally and the assembled URL's host re-checked. | Compromised/MITM upstream redirecting a download via `host@evil.com` URL confusion. |
| Destructive ops | `delete_file` / `delete_folder` carry MCP `DestructiveHint`; recursion is explicit. | Silent destructive calls the host can't warn about. |
| Auth model | OAuth only — no username/password flow. | A password sent as a URL query parameter leaks into logs and history. |
| Supply chain | Build toolchain pinned to a release with current stdlib fixes; `govulncheck` is clean and gated in CI. | Known reachable stdlib CVEs. |

Each row has a regression test that reproduces the original attack (see `*_test.go`).

## Known limitations (by design / host responsibility)

These are not bugs in this server; they are properties of the MCP trust model. Documented so operators
can reason about them:

- **Prompt injection via downloaded content.** If the agent downloads a file containing instructions
  and then acts on them, that is the *host's* trust boundary. This server validates *paths and
  arguments*, not file *content*. Keep destructive-tool confirmation enabled in your host.
- **The host approves tool calls.** Destructive tools are annotated, but enforcement of "ask the user
  before deleting" lives in the MCP host (e.g. Claude's permission prompt), not here.
- **Token scope.** A pCloud OAuth token is full-account. Treat the credentials file as a secret; revoke
  the app authorization in pCloud settings to invalidate it.
- **No per-file size cap.** A download mirrors whatever the remote tree contains; a hostile share could
  be large. Run with a destination on a volume you can afford, or review the listing first via
  `pcloud_list_folder`.

## HTTP (remote) mode

`serve --http` exposes the server to the network, so it adds attack surface that
stdio does not. Controls:

- **Bearer token required.** Every request must carry `Authorization: Bearer
  <PCLOUD_MCP_TOKEN>`; it is compared in constant time, missing/wrong returns
  `401`, and the server **refuses to start without a token** (fails closed). Use
  a long random secret (`openssl rand -hex 32`).
- **Loopback bind + TLS proxy.** The container binds `127.0.0.1` only; HTTPS is
  terminated by a reverse proxy. The port is never exposed to `0.0.0.0`.
- **Reduced tool set.** Local-filesystem tools (`download_*`, `upload_file`) are
  hidden in HTTP mode so a remote request cannot read or write the server's disk.
- **`ReadHeaderTimeout`** is set, closing the Slowloris hold-open vector.

Known limitations: the bearer token is a single shared secret (rotate by changing
the env var and restarting; revoke pCloud access from your account's app
settings). Do not place a human-login gateway (Authelia/OAuth2-Proxy) in front of
the MCP route — Claude cannot complete a login form, and the bearer token is the
access control. OAuth 2.1 (multi-user) can be layered on later without changing
the tools.

## Auditing it yourself

The minimum gate, also enforced in CI (alongside `staticcheck` and `gosec`):

```
go test ./... && go vet ./... && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```
