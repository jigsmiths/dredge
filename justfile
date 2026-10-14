BINARY_NAME := "dredge"
BINARY_PATH := "./" + BINARY_NAME
CMD_PATH := "./cmd/" + BINARY_NAME
VERSION := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
BUILD_FLAGS := "-ldflags \"-X main.version=" + VERSION + "\""

build: web-build
    @echo "Building {{BINARY_NAME}}..."
    @go build {{BUILD_FLAGS}} -o {{BINARY_PATH}} {{CMD_PATH}}
    @echo "Build complete: {{BINARY_PATH}}"

# Install frontend dependencies (no-op if there's no web/ directory)
web-install:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -d web ]; then
        cd web && npm install
    fi

# Build the frontend into web/dist (no-op if there's no web/ directory)
web-build: web-install
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -d web ]; then
        cd web && npm run build
    fi

# Run the frontend dev server (proxies /api and /health to :8080)
web-dev:
    @cd web && npm run dev

run: build
    @echo "Running {{BINARY_NAME}}..."
    @{{BINARY_PATH}}

docker:
    @docker build -t {{BINARY_NAME}} .

docker-run: docker
    @docker run --rm -p 8080:8080 {{BINARY_NAME}}

install: build
    @echo "Installing {{BINARY_NAME}} to /usr/local/bin..."
    @sudo cp {{BINARY_PATH}} /usr/local/bin/{{BINARY_NAME}}
    @echo "Installation complete: /usr/local/bin/{{BINARY_NAME}}"

test:
    @echo "Running tests..."
    @go test -race ./...

test-verbose:
    @echo "Running tests (verbose)..."
    @go test -v ./...

test-coverage:
    @echo "Running tests with coverage..."
    @go test -v -coverprofile=coverage.out ./...
    @go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated: coverage.html"

fmt:
    @echo "Formatting code..."
    @gofmt -w .
    @echo "Format complete"

lint:
    @golangci-lint run ./...

# Same checks as CI and the pre-commit hook
check:
    test -z "$(gofmt -l .)"
    just lint
    just test

# Point git at the repo hooks
setup:
    git config core.hooksPath .githooks

clean:
    @echo "Cleaning build artifacts..."
    @rm -f {{BINARY_PATH}}
    @rm -f coverage.out coverage.html
    @go clean
    @echo "Clean complete"

# Show current version and what the next version would be based on conventional commits
# Requires: go install github.com/caarlos0/svu/v3@latest
version:
    @echo "Current: $(svu current)"
    @echo "Next:    $(svu next --v0)"
