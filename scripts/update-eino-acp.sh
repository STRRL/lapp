#!/usr/bin/env bash
set -euo pipefail

MODULE="github.com/strrl/eino-acp"

echo "Updating ${MODULE} to latest commit..."
go get "${MODULE}@latest"
go mod tidy

NEW_VERSION=$(grep "${MODULE}" go.mod | awk '{print $2}')
echo "Updated to: ${NEW_VERSION}"
