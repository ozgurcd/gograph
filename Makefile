BINARY    = gograph
BUILD_DIR = bin
CMD       = ./cmd/gograph
INSTALL   = /usr/local/bin
MCPB_VERSION ?= $(shell awk -F ' = ' '$$1 == "current_version" { print $$2 }' .bumpversion.cfg)
MCPB_OUTPUT  ?= .release-mcpb
MCPB_SERVER  ?= server.json

.PHONY: build test format-check run-build clean bump-patch bump-minor bump-major install release release-verify mcpb-build mcpb-verify mcpb-smoke mcpb-check docs-check

build:
	$(eval VERSION := $(shell grep '^current_version' .bumpversion.cfg | awk '{print $$3}'))
	$(eval GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown"))
	$(eval DIRTY := $(shell git diff --quiet || echo '-dirty'))
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.version=$(VERSION)-$(GIT_COMMIT)$(DIRTY) -X main.releaseVersionMarker=gograph-release-version=/$(VERSION)-$(GIT_COMMIT)$(DIRTY)/" -o $(BUILD_DIR)/$(BINARY) $(CMD)
	@echo "Built $(BUILD_DIR)/$(BINARY) v$(VERSION)-$(GIT_COMMIT)$(DIRTY)"

release: release-verify
	@echo "Release candidate verified and server.json matches the bundles; merge the commit to main, then create the version tag explicitly."

release-verify: test mcpb-check docs-check

# install only copies whatever is already in bin/ — no implicit build.
install:
	@test -f $(BUILD_DIR)/$(BINARY) || (echo "Run 'make build' first — $(BUILD_DIR)/$(BINARY) not found." && exit 1)
	sudo rm -f $(INSTALL)/$(BINARY)
	sudo cp $(BUILD_DIR)/$(BINARY) $(INSTALL)/
	@echo "Installed $(BINARY) to $(INSTALL)/"

format-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following Go files are not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

test: build format-check
	@echo "Running all unit tests and e2e integration tests..."
	go test -v ./...
	@echo "Running race detector..."
	go test -race ./...
	@echo "Running linter..."
	golangci-lint run ./...
	@echo "Running static analysis..."
	staticcheck ./...
	@echo "Running vulnerability check..."
	go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...
	@echo "Running dependency vulnerability scan..."
	grype dir:. --fail-on high

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

test-fuzz:
	@echo "Running FuzzConstructors for 5s..."
	go test -fuzz=FuzzConstructors -fuzztime=5s ./internal/search
	@echo "Running FuzzSchema for 5s..."
	go test -fuzz=FuzzSchema -fuzztime=5s ./internal/search

mcpb-build:
	go run ./cmd/mcpb-release build --version "$(MCPB_VERSION)" --output "$(MCPB_OUTPUT)"

mcpb-verify:
	go run ./cmd/mcpb-release verify --version "$(MCPB_VERSION)" --input "$(MCPB_OUTPUT)" --server "$(MCPB_SERVER)"

mcpb-smoke:
	go run ./cmd/mcpb-release smoke --version "$(MCPB_VERSION)" --input "$(MCPB_OUTPUT)"

mcpb-check: mcpb-build
	$(MAKE) mcpb-verify MCPB_VERSION="$(MCPB_VERSION)" MCPB_OUTPUT="$(MCPB_OUTPUT)" MCPB_SERVER="$(MCPB_SERVER)"
	$(MAKE) mcpb-smoke MCPB_VERSION="$(MCPB_VERSION)" MCPB_OUTPUT="$(MCPB_OUTPUT)"

docs-check:
	hugo --source docs-site --renderToMemory --minify

run-build:
	go run $(CMD) build .

clean:
	rm -rf $(BUILD_DIR) $(MCPB_OUTPUT)

bump-patch:
	bump2version --no-commit patch --allow-dirty

bump-minor:
	bump2version --no-commit minor --allow-dirty

bump-major:
	bump2version --no-commit major --allow-dirty
