BINARY    = gograph
BUILD_DIR = bin
CMD       = ./cmd/gograph
INSTALL   = /usr/local/bin
MCPB_VERSION ?= $(shell awk -F ' = ' '$$1 == "current_version" { print $$2 }' .bumpversion.cfg)
MCPB_OUTPUT  ?= .release-mcpb
MCPB_SERVER  ?= server.json
BENCHMARK_VERSION ?= $(MCPB_VERSION)
BENCHMARK_RESULT ?= benchmarks/results/gograph-v$(BENCHMARK_VERSION).json
RELEASE_REMOTE ?= origin
RELEASE_DIST ?= $(MCPB_OUTPUT)/goreleaser-dist
GRYPE ?= grype
override GORELEASER_VERSION := v2.17.0

.PHONY: build test verify benchmark format-check vulnerability-check scan-release-artifacts release-artifact-vulnerability-check run-build clean bump-patch bump-minor bump-major install release release-dry-run release-verify release-go-check release-goreleaser-check mcpb-build mcpb-verify mcpb-smoke mcpb-check docs-check

build:
	$(eval VERSION := $(shell grep '^current_version' .bumpversion.cfg | awk '{print $$3}'))
	$(eval GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown"))
	$(eval DIRTY := $(shell git diff --quiet || echo '-dirty'))
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.version=$(VERSION)-$(GIT_COMMIT)$(DIRTY) -X main.releaseVersionMarker=gograph-release-version=/$(VERSION)-$(GIT_COMMIT)$(DIRTY)/" -o $(BUILD_DIR)/$(BINARY) $(CMD)
	@echo "Built $(BUILD_DIR)/$(BINARY) v$(VERSION)-$(GIT_COMMIT)$(DIRTY)"

benchmark: build
	go run ./scripts/benchmark.go --suite benchmarks/suite.json --gograph-bin $(BUILD_DIR)/$(BINARY) --runs 3 --output $(BENCHMARK_RESULT) --demo-output docs-site/static/demo/data.json

release:
	go run ./cmd/mcpb-release auto-release --repository-root . --remote "$(RELEASE_REMOTE)"

release-dry-run:
	go run ./cmd/mcpb-release auto-release --repository-root . --remote "$(RELEASE_REMOTE)" --dry-run

release-verify: release-go-check test docs-check release-artifact-vulnerability-check

verify: release-go-check test docs-check

release-go-check:
	go mod verify
	go mod tidy -diff
	go vet ./...

release-goreleaser-check: mcpb-check
	go run ./cmd/mcpb-release render-goreleaser --repository-root . --input .goreleaser.yaml --output "$(MCPB_OUTPUT)/.goreleaser.snapshot.yaml" --mcpb-output "$(MCPB_OUTPUT)" --dist "$(RELEASE_DIST)"
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean --skip=publish --config "$(MCPB_OUTPUT)/.goreleaser.snapshot.yaml"

release-artifact-vulnerability-check: release-goreleaser-check
	$(MAKE) scan-release-artifacts RELEASE_DIST="$(RELEASE_DIST)" GRYPE="$(GRYPE)"

scan-release-artifacts:
	@set -eu; \
		count=0; \
		for artifact in "$(RELEASE_DIST)"/gograph_*.tar.gz "$(RELEASE_DIST)"/gograph_*.zip; do \
			[ -f "$$artifact" ] || continue; \
			count=$$((count + 1)); \
		done; \
		if [ "$$count" -ne 6 ]; then \
			echo "Expected 6 freshly generated release archives, found $$count."; \
			exit 1; \
		fi; \
		for artifact in \
			"$(RELEASE_DIST)/gograph_Darwin_arm64.tar.gz" \
			"$(RELEASE_DIST)/gograph_Darwin_x86_64.tar.gz" \
			"$(RELEASE_DIST)/gograph_Linux_arm64.tar.gz" \
			"$(RELEASE_DIST)/gograph_Linux_x86_64.tar.gz" \
			"$(RELEASE_DIST)/gograph_Windows_arm64.zip" \
			"$(RELEASE_DIST)/gograph_Windows_x86_64.zip"; do \
			if [ ! -f "$$artifact" ]; then \
				echo "Missing expected release archive $$artifact."; \
				exit 1; \
			fi; \
			echo "Scanning freshly generated release archive $$artifact..."; \
			$(GRYPE) "file:$$artifact" --fail-on high; \
		done

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

test: format-check vulnerability-check
	@echo "Running all unit tests and e2e integration tests..."
	go test -count=1 -v ./...
	@echo "Running race detector..."
	go test -count=1 -race ./...
	@echo "Running linter..."
	golangci-lint run ./...
	@echo "Running static analysis..."
	staticcheck ./...
	@echo "Running vulnerability check..."
	go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...

vulnerability-check: build
	@echo "Scanning declared source dependencies..."
	$(GRYPE) file:go.mod --fail-on high
	@echo "Scanning the freshly built native binary..."
	$(GRYPE) file:$(BUILD_DIR)/$(BINARY) --fail-on high

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
