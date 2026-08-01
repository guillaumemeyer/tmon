# tmon — build, test, and release helpers
# The repo root is also the plugin directory when installed via TPM, so the
# default BIN_DIR (./bin) is exactly where the plugin expects its binary.

VERSION  := $(shell cat VERSION)
BIN_DIR  ?= ./bin
GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint cross bump bump-patch bump-minor bump-major release-check clean

build: ## Build the tmon binary into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/tmon ./cmd/tmon

test: ## Run all unit tests
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: vet ## Vet + shellcheck the shell components (if available)
	@command -v shellcheck >/dev/null 2>&1 && shellcheck tmon.tmux scripts/bootstrap.sh scripts/bump-version.sh || echo "shellcheck not installed; skipping"

cross: ## Cross-compile the release binaries into ./dist
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/tmon_$(VERSION)_linux_amd64 ./cmd/tmon
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/tmon_$(VERSION)_linux_arm64 ./cmd/tmon

bump: bump-patch ## Bump the version (default: patch)
bump-patch: ## 0.3.0 -> 0.3.1
	@./scripts/bump-version.sh patch
bump-minor: ## 0.3.0 -> 0.4.0
	@./scripts/bump-version.sh minor
bump-major: ## 0.3.0 -> 1.0.0
	@./scripts/bump-version.sh major

release-check: ## Assert the current git tag matches VERSION
	@tag="$$(git describe --tags --exact-match 2>/dev/null || true)"; \
	if [ "$$tag" != "v$(VERSION)" ]; then \
		echo "error: tag $$tag != v$(VERSION)"; exit 1; \
	fi

clean: ## Remove build artifacts and runtime state
	rm -rf bin state dist
