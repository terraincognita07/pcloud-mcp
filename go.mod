module github.com/terraincognita07/pcloud-mcp

go 1.26

// Pin the build toolchain to a release with the latest stdlib security fixes.
// Bumped to 1.26.4 for GO-2026-5039 (net/textproto) and GO-2026-5037
// (crypto/x509), both reachable from the OAuth paths and fixed in 1.26.4.
toolchain go1.26.4

require github.com/modelcontextprotocol/go-sdk v1.6.1

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
)
