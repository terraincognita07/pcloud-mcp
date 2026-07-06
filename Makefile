# pcloud-mcp — common development targets.
# Run `make help` for the list.

BINARY := pcloud-mcp
PKG    := ./...

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary
	go build -trimpath -o $(BINARY) ./cmd/pcloud-mcp

.PHONY: test
test: ## Run all tests
	go test -count=1 $(PKG)

.PHONY: cover
cover: ## Run tests with a coverage summary
	go test -covermode=atomic -coverprofile=coverage.out $(PKG)
	go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	go tool cover -html=coverage.out

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint (includes staticcheck)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run $(PKG)

.PHONY: sec
sec: ## Run govulncheck + gosec
	go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 $(PKG)
	go run github.com/securego/gosec/v2/cmd/gosec@v2.26.1 -exclude-generated $(PKG)

.PHONY: check
check: vet lint test ## Vet, lint, and test — the pre-commit gate

.PHONY: docker
docker: ## Build the container image
	docker build -t $(BINARY) .

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) $(BINARY).exe coverage.out
