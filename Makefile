# Goa — Terminal-native AI coding agent
#
# Targets:
#   build        Build the Goa binary (default)
#   clean        Remove all build artifacts
#   test         Run all tests with race detection and coverage
#   test-short   Run tests without race detection (fast)
#   test-cover   Generate HTML coverage report
#   test-race    Run tests with race detection only
#   lint         Run gocognit + gocyclo complexity checks
#   vet          Run go vet
#   fmt          Format all Go source files
#   install      Install to GOPATH/bin
#   cross        Cross-compile for Linux, macOS, Windows
#   run          Run Goa in current directory
#   web-build    Render docs/*.md to web/*.html (builds the site)
#   web-clean    Remove generated web/*.html doc pages
#   web-serve    Build the site and serve it locally for preview
#   help         Print this help message

.PHONY: build clean test test-short test-cover test-race lint vet fmt install cross run web-build web-clean web-serve help

GO := go
BINARY := goa
LD_FLAGS := -ldflags="-s -w"
MODULE := github.com/yourorg/goa
GO_PACKAGES := ./cmd/... ./config/... ./core/... ./internal/... ./memory/... \
               ./multiagent/... ./plugins/... ./profiles/... ./provider/... \
               ./skills/... ./tools/... ./tui/...

# ── Build ────────────────────────────────────────────────────────────────

# models regenerates the model catalog (internal/agentic/provider/models/
# models_generated.go) from models.dev. Best-effort: when models.dev is
# unreachable, the generator warns and keeps the checked-in catalog, so
# offline builds still work. Runs on every build to keep model metadata
# (context windows, pricing, new models) current.
models:
	$(GO) generate ./internal/agentic/provider/models/

build: clean models
	$(GO) build $(LD_FLAGS) -o $(BINARY) ./cmd/goa

# ── Clean ────────────────────────────────────────────────────────────────

clean:
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf dist/

# ── Test ─────────────────────────────────────────────────────────────────

test: fmt vet
	$(GO) test -count=1 -race -cover ./...

test-short:
	$(GO) test -count=1 -short ./...

test-race:
	$(GO) test -count=1 -race ./...

test-cover:
	$(GO) test -count=1 -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ── Quality ──────────────────────────────────────────────────────────────

lint:
	@echo "=== Cognitive complexity (gocognit) ==="
	@command -v gocognit >/dev/null 2>&1 || { echo "gocognit not installed. Run: go install github.com/uudashr/gocognit/cmd/gocognit@latest"; exit 1; }
	@find . -type f -name '*.go' -not -path './agentic/*' -not -path './vendor/*' -not -path './.goa/*' -print0 | xargs -0 gocognit -over 15 || true
	@echo ""
	@echo "=== Cyclomatic complexity (gocyclo) ==="
	@command -v gocyclo >/dev/null 2>&1 || { echo "gocyclo not installed. Run: go install github.com/fzipp/gocyclo/cmd/gocyclo@latest"; exit 1; }
	@find . -type f -name '*.go' -not -path './agentic/*' -not -path './vendor/*' -not -path './.goa/*' -print0 | xargs -0 gocyclo -over 12 || true

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

# ── Install ──────────────────────────────────────────────────────────────

install:
	$(GO) install $(LD_FLAGS) ./cmd/goa

# ── Cross-compile ────────────────────────────────────────────────────────

cross: models
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 $(GO) build $(LD_FLAGS) -o dist/$(BINARY)-linux-amd64 ./cmd/goa
	GOOS=linux   GOARCH=arm64 $(GO) build $(LD_FLAGS) -o dist/$(BINARY)-linux-arm64 ./cmd/goa
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LD_FLAGS) -o dist/$(BINARY)-darwin-amd64 ./cmd/goa
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LD_FLAGS) -o dist/$(BINARY)-darwin-arm64 ./cmd/goa
	GOOS=windows GOARCH=amd64 $(GO) build $(LD_FLAGS) -o dist/$(BINARY)-windows-amd64.exe ./cmd/goa
	@echo "Cross-compiled binaries in dist/:"
	@ls -lh dist/

# ── Agentic Demos ──────────────────────────────────────────────────────

# agentic-demo builds and runs all agentic SDK demo programs.
# Each demo has a //go:build ignore tag. Use -tags=ignore to build.
agentic-demo:
	@echo "Building agentic demo programs..."
	$(GO) build -tags=ignore ./internal/agentic/demo/...
	@echo "All demos built successfully."
	@echo ""
	@echo "Available demos:"
	for d in simple api context-compress deepskill inline-skill inline-test plan-review skill stream-xml-demo; do \
		echo "  $$d: go run -tags=ignore ./internal/agentic/demo/$$d/ --help"; \
	done

agentic-demo-%:
	$(GO) run -tags=ignore ./internal/agentic/demo/$*/ --help

# ── Run ──────────────────────────────────────────────────────────────────

run: build
	./$(BINARY)

# ── Website ───────────────────────────────────────────────────────────────
# The landing page (web/index.html), assets and the demo gif are committed.
# Documentation pages are generated at build time from docs/*.md by the
# cmd/webbuild tool, so the published site renders documentation instead of
# linking to raw GitHub source.

WEB_BUILD := $(GO) run ./cmd/webbuild -in docs -out web
WEB_GEN_GLOB := architecture.html setup.html configuration.html commands.html \
                tools.html skills.html tui.html profiles.html docs.html \
                agentic-sdk.html plugins.html providers.html workflows.html \
                orchestration-design.html skill-execution.html development.html \
                profiling.html goals.html

web-build:
	$(WEB_BUILD)

web-clean:
	rm -f $(addprefix web/,$(WEB_GEN_GLOB))

web-serve: web-build
	@echo "Serving web/ at http://localhost:8000 (Ctrl-C to stop)"
	cd web && python3 -m http.server 8000

# ── Help ─────────────────────────────────────────────────────────────────

help:
	@echo "Goa — Terminal-native AI coding agent"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Build targets:"
	@echo "  build        Build the Goa binary"
	@echo "  clean        Remove all build artifacts"
	@echo "  install      Install to GOPATH/bin"
	@echo "  cross        Cross-compile for all platforms"
	@echo "  run          Build and run Goa"
	@echo ""
	@echo "Website targets:"
	@echo "  web-build    Render docs/*.md into web/*.html"
	@echo "  web-clean    Remove generated web doc pages"
	@echo "  web-serve    Build and serve the site locally"
	@echo ""
	@echo "Test targets:"
	@echo "  test         Run all tests with race detection and coverage"
	@echo "  test-short   Run tests (short mode, no race)"
	@echo "  test-race    Run tests with race detection"
	@echo "  test-cover   Generate HTML coverage report"
	@echo "  test-e2e     Run E2E tests (PTY-based, requires build tag e2e)"
	@echo ""
	@echo "Quality targets:"
	@echo "  lint         Run complexity checks (gocognit + gocyclo)"
	@echo "  vet          Run go vet"
	@echo "  fmt          Format all Go source files"
