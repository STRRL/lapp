default:
    @just --list

# Build the CLI binary
build:
    go build -o output/lapp ./cmd/lapp/

# Run the CLI (pass args directly, e.g. just run ingest foo.log --db bar.duckdb)
run *args:
    go run ./cmd/lapp/ {{args}}

# Run all unit tests
test:
    go test ./...

# Run integration test (requires LOGHUB_PATH)
test-integration:
    go test -v -timeout 15m -count=1 ./integration_test/

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
ci: fmt vet build lint test

# Tidy go modules
tidy:
    go mod tidy

# Run prek on all files
prek-all:
    prek run --all-files

# Install prek git hooks
prek-install:
    prek install

# Ingest a log file
ingest file db="lapp.duckdb":
    go run ./cmd/lapp/ ingest {{file}} --db {{db}}

# List discovered templates
templates db="lapp.duckdb":
    go run ./cmd/lapp/ templates --db {{db}}

# Query logs by template ID
query template db="lapp.duckdb":
    go run ./cmd/lapp/ query --template {{template}} --db {{db}}
