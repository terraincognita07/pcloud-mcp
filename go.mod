module github.com/terraincognita07/pcloud-mcp

go 1.26

// Pin the build toolchain to a release with the latest stdlib security fixes
// (crypto/tls, crypto/x509, net/http2); govulncheck is clean as of 1.26.3.
toolchain go1.26.3

require github.com/modelcontextprotocol/go-sdk v1.6.1

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)
