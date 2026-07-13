VERSION ?= 0.3.0-alpha.2
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILDINFO := github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT) -X $(BUILDINFO).Date=$(BUILD_DATE)
GOVULNCHECK ?= $(shell go env GOPATH)/bin/govulncheck

.PHONY: build test check vuln cross-build clean

build:
	mkdir -p build
	go build -trimpath -ldflags '$(LDFLAGS)' -o build/jellyfin-remora ./cmd/jellyfin-remora
	go build -trimpath -ldflags '$(LDFLAGS)' -o build/remoractl ./cmd/remoractl

test:
	go test -race ./...

check:
	go test ./...
	go vet ./...

vuln:
	@test -x "$(GOVULNCHECK)" || (echo "govulncheck is missing; run: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2; exit 1)
	"$(GOVULNCHECK)" ./...

cross-build:
	@set -eu; \
	for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o build/jellyfin-remora-$$os-$$arch$$ext ./cmd/jellyfin-remora; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o build/remoractl-$$os-$$arch$$ext ./cmd/remoractl; \
	done

clean:
	rm -rf build coverage.out
