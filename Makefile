.PHONY: build install test lint fmt clean tidy rpm patch minor major check-go e2e e2e-vault e2e-openbao e2e-up e2e-down

BINARY := vault-plugin-secrets-rancher
PKG := ./cmd/vault-plugin-secrets-rancher
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
PLUGIN_DIR ?= ./dist

GO_VERSION_REQUIRED := 1.25
GO_VERSION_ACTUAL := $(shell go version | sed 's/go version go\([0-9]*\.[0-9]*\).*/\1/')

check-go:
	@if [ "$$(printf '%s\n%s' "$(GO_VERSION_REQUIRED)" "$(GO_VERSION_ACTUAL)" | sort -V | head -1)" != "$(GO_VERSION_REQUIRED)" ]; then \
		echo "ERROR: Go >= $(GO_VERSION_REQUIRED) required, found $(GO_VERSION_ACTUAL)"; exit 1; \
	fi

build: check-go tidy
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(PLUGIN_DIR)/$(BINARY) $(PKG)

install: build
	@echo "Built $(PLUGIN_DIR)/$(BINARY) (register it with: vault plugin register -sha256=<sha> secret $(BINARY))"

test: check-go
	go test -race -count=1 ./...

lint: check-go
	go vet ./...

fmt: check-go
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf $(PLUGIN_DIR)

rpm:
	GOTOOLCHAIN=auto go run github.com/goreleaser/goreleaser/v2@v2.20.2 release --snapshot --clean --skip=sign

# End-to-end tests bring up a mock companion API plus Vault and OpenBao in
# Docker and drive the full lifecycle against each with the same plugin binary.
e2e:
	./scripts/e2e.sh

e2e-vault:
	ENGINES=vault ./scripts/e2e.sh

e2e-openbao:
	ENGINES=openbao ./scripts/e2e.sh

e2e-up:
	docker compose -f test/docker-compose.yml up -d --build

e2e-down:
	docker compose -f test/docker-compose.yml down -v

DEFAULT_BRANCH ?= main

# Draft the release with generated notes, then push the tag; the release
# workflow attaches assets to the draft and publishes it.
patch minor major:
	@git fetch --quiet --tags origin $(DEFAULT_BRANCH)
	@test "$$(git branch --show-current)" = "$(DEFAULT_BRANCH)" || { echo "ERROR: not on $(DEFAULT_BRANCH)"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "ERROR: working tree not clean"; exit 1; }
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/$(DEFAULT_BRANCH))" || { echo "ERROR: HEAD is not origin/$(DEFAULT_BRANCH)"; exit 1; }
	@LATEST=$$(git tag --list 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1); \
	NEW=$$(echo "$${LATEST:-v0.0.0}" | sed 's/^v//' | awk -F. -v b=$@ '{ \
		if (b == "major") print "v" ($$1+1) ".0.0"; \
		else if (b == "minor") print "v" $$1 "." ($$2+1) ".0"; \
		else print "v" $$1 "." $$2 "." ($$3+1) }'); \
	SHA=$$(git rev-parse HEAD); \
	gh release create "$$NEW" --draft --target "$$SHA" --generate-notes --title "$$NEW" || exit 1; \
	git push origin "$$SHA:refs/tags/$$NEW" || { gh release delete "$$NEW" --yes; exit 1; }
