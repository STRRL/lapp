.DEFAULT_GOAL := help

WEB_ADDR ?= 127.0.0.1:8080

.PHONY: help build clean dev check run unit-test integration-test test fmt vet lint ci tidy proto-gen proto-lint frontend-typecheck web-assets prek-all prek-install

# Show available commands
help:
	@echo "Available targets:"
	@echo "  make build              Build embedded frontend assets and output/lapp"
	@echo "  make clean              Remove generated build artifacts"
	@echo "  make dev                Clean, build, and start the local web app"
	@echo "  make proto-gen          Generate protobuf/Connect code"
	@echo "  make test               Run all tests"
	@echo "  make check              Run all checks"

# Build embedded frontend assets and the CLI binary
build: web-assets
	go build -o output/lapp ./cmd/lapp/

# Remove generated build artifacts
clean:
	rm -rf output frontend/dist frontend/.vite pkg/webapp/static/app pkg/webapp/static/assets

# Clean, build, and start the local web app
dev: clean build
	./output/lapp web --addr $(WEB_ADDR)

# Generate protobuf/Connect code
proto-gen:
	buf generate

# Lint protobuf schemas
proto-lint:
	buf lint

# Type-check the web frontend
frontend-typecheck:
	cd frontend && pnpm typecheck

# Build frontend assets into pkg/webapp/static for embedding
web-assets:
	cd frontend && pnpm build

# Run unit tests only
unit-test:
	go test ./pkg/...

# Run integration tests (requires LOGHUB_PATH)
integration-test:
	go test -v -timeout 15m -count=1 ./integration_test/...

# Run all tests (unit + integration)
test: unit-test integration-test

# Format Go code
fmt:
	gofmt -l -w .

# Run go vet
vet:
	go vet ./...

# Run golangci-lint
lint:
	golangci-lint run

# Run local checks
check: tidy fmt vet proto-lint frontend-typecheck build lint unit-test

# Run all CI checks (same as pre-commit)
ci: check

# Tidy go modules
tidy:
	go mod tidy

# Run prek on all files
prek-all:
	prek run --all-files

# Install prek git hooks
prek-install:
	prek install
