BINARY  := subnet-migrator
PKG     := ./cmd/subnet-migrator
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version.Tool=$(VERSION)
DIST    := dist

# CGO off keeps the binaries static and lets Windows cross-compile with no
# extra toolchain.
export CGO_ENABLED := 0

.PHONY: build
build: build-linux build-windows

.PHONY: build-linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/linux-amd64/$(BINARY) $(PKG)

.PHONY: build-windows
build-windows:
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/windows-amd64/$(BINARY).exe $(PKG)

# Host-platform build for quick local iteration.
.PHONY: dev
dev:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -w $(shell git ls-files '*.go')

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: clean
clean:
	rm -rf $(DIST) bin
