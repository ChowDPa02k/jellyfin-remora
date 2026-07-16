VERSION ?= 0.8.0-alpha.9
COMMIT ?= $(shell commit=$$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown); \
	if [ -n "$$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then commit="$$commit-dirty"; fi; \
	printf '%s' "$$commit")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILDINFO := github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT) -X $(BUILDINFO).Date=$(BUILD_DATE)
GOVULNCHECK ?= $(shell go env GOPATH)/bin/govulncheck
BUILD_ROOT ?= build

.PHONY: build test check vuln cross-build package-linux-tar package-linux-deb package-linux-rpm clean

build:
	@set -eu; \
	os=$$(go env GOOS); arch=$$(go env GOARCH); dir_arch=$$arch; ext=""; \
	if [ "$$arch" = amd64 ]; then dir_arch=x86_64; fi; \
	if [ "$$os" = windows ]; then ext=.exe; fi; \
	dir="$(BUILD_ROOT)/$$os/$$dir_arch"; \
	mkdir -p "$$dir"; \
	go build -trimpath -ldflags '$(LDFLAGS)' -o "$$dir/jellyfin-remora$$ext" ./cmd/jellyfin-remora; \
	go build -trimpath -ldflags '$(LDFLAGS)' -o "$$dir/remoractl$$ext" ./cmd/remoractl

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
	for target in darwin/arm64 linux/arm64 linux/amd64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; dir_arch=$$arch; ext=""; \
		if [ "$$arch" = amd64 ]; then dir_arch=x86_64; fi; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		dir="$(BUILD_ROOT)/$$os/$$dir_arch"; \
		mkdir -p "$$dir"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o "$$dir/jellyfin-remora$$ext" ./cmd/jellyfin-remora; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o "$$dir/remoractl$$ext" ./cmd/remoractl; \
	done

package-linux-tar:
	@for arch in amd64 arm64; do \
		SOURCE_DATE_EPOCH="$${SOURCE_DATE_EPOCH:-$$(date +%s)}" \
			./packaging/linux/package-tar.sh "$(VERSION)" "$$arch"; \
	done

package-linux-deb:
	./packaging/linux/package-native.sh "$(VERSION)" "$${GOARCH:-$$(go env GOARCH)}" deb

package-linux-rpm:
	./packaging/linux/package-native.sh "$(VERSION)" "$${GOARCH:-$$(go env GOARCH)}" rpm

clean:
	rm -rf "$(BUILD_ROOT)" coverage.out
