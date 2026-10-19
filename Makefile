# All Go tooling runs inside Docker; nothing is installed on the host.
BINARY    := azd
GO_IMAGE  ?= golang:1.26-alpine
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

HOST_OS   := $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
GOOS      ?= $(HOST_OS)
GOARCH    ?= $(HOST_ARCH)

PREFIX    ?= $(HOME)/.local
# DOCKER_RUN runs natively inside the container (tests, vet); DOCKER_GO
# cross-compiles for $(GOOS)/$(GOARCH).
DOCKER_RUN = docker run --rm \
	-v "$(CURDIR)":/src -w /src \
	-v azd-gomod:/go/pkg/mod -v azd-gocache:/root/.cache/go-build \
	-e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false
DOCKER_GO  = $(DOCKER_RUN) -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) $(GO_IMAGE)
DOCKER_NATIVE = $(DOCKER_RUN) $(GO_IMAGE)

.PHONY: build cross test vet fmt tidy image run-docker install uninstall snapshot clean help

help: ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build: ## Build bin/azd for this machine (via Docker)
	$(DOCKER_GO) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/azd
	@echo "built bin/$(BINARY) ($(GOOS)/$(GOARCH))"

cross: ## Build linux/darwin x amd64/arm64 into dist/
	@for os in linux darwin; do for arch in amd64 arm64; do \
		echo "-> $$os/$$arch"; \
		$(MAKE) --no-print-directory build-one GOOS=$$os GOARCH=$$arch || exit 1; \
	done; done

build-one:
	$(DOCKER_GO) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(GOOS)_$(GOARCH) ./cmd/azd

test: ## Run tests (via Docker)
	$(DOCKER_NATIVE) go test -race=false ./...

vet: ## Run go vet for linux and darwin (via Docker)
	$(DOCKER_NATIVE) go vet ./...
	$(DOCKER_RUN) -e GOOS=darwin $(GO_IMAGE) go vet ./...

fmt: ## gofmt the tree (via Docker)
	$(DOCKER_NATIVE) gofmt -w cmd internal

tidy: ## go mod tidy (via Docker)
	$(DOCKER_NATIVE) go mod tidy

image: ## Build the runtime container image (includes Azure CLI for auth)
	docker build --build-arg VERSION=$(VERSION) -t azd:$(VERSION) -t azd:latest .

run-docker: image ## Run azd in Docker using your host ~/.azure login
	docker run --rm -it -v "$(HOME)/.azure":/root/.azure -v azd-config:/root/.config/azd azd:latest

install: build ## Install bin/azd into $(PREFIX)/bin (user-local, no sudo)
	install -d "$(PREFIX)/bin"
	install -m 0755 bin/$(BINARY) "$(PREFIX)/bin/$(BINARY)"
	@echo "installed $(PREFIX)/bin/$(BINARY)"

uninstall: ## Remove $(PREFIX)/bin/azd
	rm -f "$(PREFIX)/bin/$(BINARY)"

snapshot: ## Build release archives locally with GoReleaser (via Docker)
	docker run --rm -v "$(CURDIR)":/src -w /src -v azd-gomod:/go/pkg/mod \
		goreleaser/goreleaser:latest release --snapshot --clean

clean: ## Remove build outputs
	rm -rf bin dist
