#!/bin/bash
set -e

# cd で作業ディレクトリが変わると $0 の相対パスが解決できなくなるため、
# 最初にリポジトリルートを絶対パスで確定させておく。
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "=== Running pre-commit checks ==="

# Backend checks
echo ""
echo "--- Backend ---"
cd "$ROOT/backend"

echo "Running Go tests..."
go test ./...

echo "Running golangci-lint..."
golangci-lint run

# Frontend checks
echo ""
echo "--- Frontend ---"
cd "$ROOT/frontend"

echo "Running type check..."
npm run type-check

echo "Running lint..."
npm run lint

echo "Running tests..."
npm test

echo ""
echo "=== All checks passed! ==="
