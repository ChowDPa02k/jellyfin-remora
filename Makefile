.PHONY: build test check clean

build:
	mkdir -p build
	go build -trimpath -o build/jellyfin-remora ./cmd/jellyfin-remora
	go build -trimpath -o build/remoractl ./cmd/remoractl

test:
	go test -race ./...

check:
	go test ./...
	go vet ./...

clean:
	rm -rf build coverage.out
