.PHONY: build run unit-test integration-test test fmt vet lint ci tidy prek-all prek-install

# Build the CLI binary
build:
	go build -o output/lapp ./cmd/lapp/

# Run unit tests only
unit-test:
	go test ./pkg/...

# Run integration tests (requires LOGHUB_PATH)
integration-test:
	go test -v -timeout 15m -count=1 ./integration_test/

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

# Run all CI checks (same as pre-commit)
ci: fmt vet build lint unit-test

# Tidy go modules
tidy:
	go mod tidy

# Run prek on all files
prek-all:
	prek run --all-files

# Install prek git hooks
prek-install:
	prek install
