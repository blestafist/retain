SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:

.DEFAULT_GOAL := help
.PHONY: help dev web-deps gen test lint build llm-smoke

help: ## Show available targets
	@awk 'BEGIN { FS = ":.*##"; print "Available targets:" } /^[a-zA-Z0-9_-]+:.*##/ { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

dev: web/node_modules ## Run the Go server and Vite dev server; stop both on exit
	PUBLIC_ORIGIN=http://localhost:5173 setsid go run ./cmd/retain &
	backend_pid=$$!
	setsid npm --prefix web run dev -- --host 127.0.0.1 &
	frontend_pid=$$!
	cleanup() {
		kill -- "-$$backend_pid" "-$$frontend_pid" 2>/dev/null || true
		wait "$$backend_pid" "$$frontend_pid" 2>/dev/null || true
	}
	trap cleanup EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM
	wait -n "$$backend_pid" "$$frontend_pid"

web/node_modules: web/package.json web/package-lock.json
	npm ci --prefix web

web-deps: web/node_modules ## Install locked frontend dependencies

gen: ## Generate Go database code with sqlc
	sqlc generate

test: web/node_modules ## Run Go and frontend tests
	go test ./...
	npm --prefix web test

lint: web/node_modules ## Run golangci-lint, ESLint, and TypeScript checks
	if command -v golangci-lint >/dev/null 2>&1; then
		golangci-lint run
	else
		echo "golangci-lint is not installed; running go vet ./... instead" >&2
		go vet ./...
	fi
	npm --prefix web run lint
	npm --prefix web run typecheck

build: web/node_modules ## Build the frontend and a static Go binary with embedded assets
	npm --prefix web run build
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -o bin/retain ./cmd/retain

llm-smoke: ## Manually verify the configured real LLM provider
	go run ./cmd/llm-smoke
