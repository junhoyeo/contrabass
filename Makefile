# Contrabass — Build Tooling
# Build order: dashboard SPA must build before Go binary (embed.FS requires dist/)

.PHONY: build-dashboard build dev-dashboard dev test test-dashboard test-all clean lint

# Build the React dashboard SPA to packages/dashboard/dist/
build-dashboard:
	cd packages/dashboard && bun run build

# Build the Go binary with embedded dashboard
build: build-dashboard
	go build -o contrabass ./cmd/contrabass

# Start Vite dev server for dashboard development (with hot reload)
dev-dashboard:
	cd packages/dashboard && bun run dev

# Run Go binary in dev mode
dev:
	go run ./cmd/contrabass --port 8080

# Run all Go tests
test:
	go test ./... -count=1

# Run React dashboard tests
test-dashboard:
	cd packages/dashboard && bun test

# Run all tests (Go + React)
test-all: test test-dashboard

# Remove build artifacts
clean:
	rm -rf packages/dashboard/dist packages/landing/dist contrabass

# Run Go linter
lint:
	go vet ./...
