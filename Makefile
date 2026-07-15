.PHONY: build install test lint fmt clean tidy rpm rpm-package patch minor major check-go e2e e2e-vault e2e-openbao e2e-up e2e-down

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

rpm: build rpm-package

rpm-package:
	./scripts/build-rpm.sh $(VERSION)

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

_LATEST := $(shell git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1)
_BASE   := $(if $(_LATEST),$(_LATEST),v0.0.0)
_MAJ    := $(shell echo $(_BASE) | sed 's/^v//' | cut -d. -f1)
_MIN    := $(shell echo $(_BASE) | sed 's/^v//' | cut -d. -f2)
_PAT    := $(shell echo $(_BASE) | sed 's/^v//' | cut -d. -f3)

patch:
	@NEW=v$(_MAJ).$(_MIN).$(shell expr $(_PAT) + 1); \
	git tag $$NEW && echo "Tagged $$NEW" && git push origin $$NEW

minor:
	@NEW=v$(_MAJ).$(shell expr $(_MIN) + 1).0; \
	git tag $$NEW && echo "Tagged $$NEW" && git push origin $$NEW

major:
	@NEW=v$(shell expr $(_MAJ) + 1).0.0; \
	git tag $$NEW && echo "Tagged $$NEW" && git push origin $$NEW
