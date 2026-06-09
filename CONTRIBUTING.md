# Contributing to pcloud-mcp

Thanks for your interest. This is a small, security-focused project; the bar for
changes is correctness and not weakening the trust boundary.

## Before you start

- For anything non-trivial, open an issue first so we don't duplicate work.
- For a **security issue**, do not open a public issue — see [SECURITY.md](SECURITY.md).

## Development setup

You need Go 1.26+ (the build toolchain is pinned in `go.mod`).

```sh
git clone https://github.com/terraincognita07/pcloud-mcp.git
cd pcloud-mcp
make check      # vet + staticcheck + tests — the pre-commit gate
```

Common targets (`make help` for the full list):

| Target | What it does |
|---|---|
| `make build` | build the binary |
| `make test` | run all tests |
| `make cover` | tests + coverage summary |
| `make check` | vet + staticcheck + test (run this before a PR) |
| `make sec` | govulncheck + gosec |

## Architecture rules

The code is layered **one-directionally**:

```
cmd → mcpserver → {download, pcloud, safepath}
    ↘ oauth, config   (wired in cmd; not imported by mcpserver)
    ↘ httpserver      (network boundary for HTTP mode)
```

- `safepath` and `pcloud` must not import filesystem-layout or MCP concerns —
  the security boundary stays in one auditable place.
- Tool handlers (`mcpserver`) own request shape and response mapping only;
  domain behavior lives in the inner packages.
- Prefer the smallest correct change. This repo is deliberately near-zero-dep
  (stdlib + the official MCP SDK); justify any new dependency.

## Security invariants — do not weaken

These each have a regression test that reproduces the original attack. **Never
delete a security test to make a change pass.** If an invariant must change,
change the test deliberately and say so in the PR.

- **Path containment:** every remote name goes through `safepath.SafeName`
  *after* decoding, and every local write goes through an `os.Root` scoped to
  the destination. Never `os.OpenFile`/`os.ReadFile` a remote-derived path
  directly.
- **Token handling:** the access token travels in the POST body (never the URL),
  is stored `0600`, and is never logged or printed. `String()` methods redact it.
- **OAuth:** loopback bind only, random `state` compared in constant time, a
  bogus callback must not abort the flow.
- **Destructive tools** (`delete_*`) keep their MCP `DestructiveHint`.

## Pull request checklist

- [ ] `make check` passes (vet, staticcheck, tests)
- [ ] new behavior has tests; hostile-input paths have a refusal test
- [ ] `gofmt`-clean (CI enforces this)
- [ ] no secrets, tokens, or full user paths in code, logs, or fixtures
- [ ] CHANGELOG.md updated for user-visible changes

CI runs build, vet, `gofmt`, race-enabled tests with coverage, `govulncheck`,
`staticcheck`, `gosec`, and a non-root Docker build. All must pass.
