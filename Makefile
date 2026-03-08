# Contrabass — Build Tooling
# Build order: dashboard SPA must build before Go binary (embed.FS requires dist/)

.PHONY: build-dashboard build-landing build dev-dashboard dev-landing dev test test-race test-cover test-dashboard test-landing test-quick test-all ci clean lint release-dry

# Build the React dashboard SPA to packages/dashboard/dist/
build-dashboard:
	cd packages/dashboard && bun run build && touch dist/.gitkeep

# Build the Astro landing site to packages/landing/dist/
build-landing:
	cd packages/landing && bun run build

# Build the Go binary with embedded dashboard
build: build-dashboard
	go build -ldflags "-X main.version=dev -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o contrabass ./cmd/contrabass

# Start Vite dev server for dashboard development (with hot reload)
dev-dashboard:
	cd packages/dashboard && bun run dev

# Start Astro dev server for landing page development
dev-landing:
	cd packages/landing && bun run dev

# Run Go binary in dev mode
dev:
	go run ./cmd/contrabass --port 8080

# Run all Go tests
test:
	go test ./... -count=1

# Run Go tests with race detector
test-race:
	go test -race ./... -count=1

# Run Go tests with coverage for critical packages
test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./internal/team/... ./internal/orchestrator/... ./internal/agent/...
	go tool cover -func=coverage.out | tail -1

# Run React dashboard tests
test-dashboard:
	cd packages/dashboard && bun test

# Run Astro landing checks
test-landing:
	cd packages/landing && bun run check

# Run the recommended local validation path
test-quick: test test-dashboard test-landing

# Run all tests/checks
test-all: test-quick

# Run the preferred CI/local full validation flow
# Dashboard must be built first: embed_dashboard.go requires packages/dashboard/dist/
ci:
	$(MAKE) build-dashboard
	$(MAKE) lint
	$(MAKE) test-quick
	$(MAKE) build
	$(MAKE) build-landing

# Remove build artifacts
clean:
	rm -rf packages/dashboard/dist packages/landing/dist contrabass
	mkdir -p packages/dashboard/dist
	touch packages/dashboard/dist/.gitkeep

# Run Go linter
lint:
	go vet ./...

# Dry-run GoReleaser locally (skips publish)
release-dry: build-dashboard
	goreleaser release --snapshot --clean
