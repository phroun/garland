.PHONY: all build test test-verbose test-coverage test-race lint fmt vet clean help repl

# Default target
all: fmt vet test build

# Build the package
build:
	go build -v ./...

# Build and run the REPL
repl:
	go build -o garland-repl ./cmd/garland-repl && ./garland-repl

# Build the REPL only
build-repl:
	go build -o garland-repl ./cmd/garland-repl

# Run tests
test:
	go test ./...

# Run tests with verbose output
test-verbose:
	go test -v ./...

# Run tests with coverage report
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run tests with race detector
test-race:
	go test -race ./...

# Run all tests (verbose, race, coverage)
test-all: test-verbose test-race test-coverage

# Run linter (requires golangci-lint)
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Clean build artifacts
clean:
	go clean ./...
	rm -f coverage.out coverage.html

# Run benchmarks
bench:
	go test -bench=. -benchmem ./...

# Generate documentation
doc:
	@echo "Starting godoc server at http://localhost:6060/pkg/github.com/phroun/garland/"
	godoc -http=:6060

# Tidy dependencies
tidy:
	go mod tidy

# Check for vulnerabilities (requires govulncheck)
vuln:
	@which govulncheck > /dev/null || (echo "Installing govulncheck..." && go install golang.org/x/vuln/cmd/govulncheck@latest)
	govulncheck ./...

# Help
help:
	@echo "Garland Makefile targets:"
	@echo "  all           - Format, vet, test, and build (default)"
	@echo "  build         - Build the package"
	@echo "  test          - Run tests"
	@echo "  test-verbose  - Run tests with verbose output"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  test-race     - Run tests with race detector"
	@echo "  test-all      - Run all test variants"
	@echo "  lint          - Run golangci-lint"
	@echo "  fmt           - Format code"
	@echo "  vet           - Run go vet"
	@echo "  bench         - Run benchmarks"
	@echo "  clean         - Clean build artifacts"
	@echo "  doc           - Start godoc server"
	@echo "  tidy          - Tidy dependencies"
	@echo "  vuln          - Check for vulnerabilities"
	@echo "  help          - Show this help"
