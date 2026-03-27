GO ?= go
OPUS_TAGS ?= nolibopusfile
WINDOWS_CC ?= x86_64-w64-mingw32-gcc
WINDOWS_PKG_CONFIG ?= x86_64-w64-mingw32-pkg-config
WINDOWS_OPUS_ROOT ?= third_party/opus/windows/x64
WINDOWS_OPUS_INCLUDE ?= $(WINDOWS_OPUS_ROOT)/include
WINDOWS_OPUS_LIB ?= $(WINDOWS_OPUS_ROOT)/lib
WINDOWS_OPUS_PKGCONFIG ?= $(WINDOWS_OPUS_LIB)/pkgconfig
BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/server
CLIENT_BIN := $(BIN_DIR)/client
SERVER_BIN_LINUX := $(BIN_DIR)/server-linux
CLIENT_BIN_LINUX := $(BIN_DIR)/client-linux
SERVER_BIN_WINDOWS := $(BIN_DIR)/server-windows.exe
CLIENT_BIN_WINDOWS := $(BIN_DIR)/client-windows.exe

.PHONY: build build-server build-client run-server run-client test clean
.PHONY: all build build-server build-client run-server run-client test clean
.PHONY: build-linux build-windows build-windows-static check-windows-static-env
.PHONY: build\:linux build\:windows

all: build test

build: build-server build-client

build-linux:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -tags "$(OPUS_TAGS)" -o $(SERVER_BIN_LINUX) ./cmd/server
	GOOS=linux GOARCH=amd64 $(GO) build -tags "$(OPUS_TAGS)" -o $(CLIENT_BIN_LINUX) ./cmd/client

build-windows:
	mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -tags "$(OPUS_TAGS)" -o $(SERVER_BIN_WINDOWS) ./cmd/server
	GOOS=windows GOARCH=amd64 $(GO) build -tags "$(OPUS_TAGS)" -o $(CLIENT_BIN_WINDOWS) ./cmd/client

check-windows-static-env:
	@test -d "$(WINDOWS_OPUS_INCLUDE)" || (echo "missing opus include dir: $(WINDOWS_OPUS_INCLUDE)"; exit 1)
	@test -d "$(WINDOWS_OPUS_LIB)" || (echo "missing opus lib dir: $(WINDOWS_OPUS_LIB)"; exit 1)
	@test -f "$(WINDOWS_OPUS_LIB)/libopus.a" || (echo "missing static lib: $(WINDOWS_OPUS_LIB)/libopus.a"; exit 1)

build-windows-static: check-windows-static-env
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
	CC="$(WINDOWS_CC)" PKG_CONFIG="$(WINDOWS_PKG_CONFIG)" \
	PKG_CONFIG_PATH="$(WINDOWS_OPUS_PKGCONFIG)" PKG_CONFIG_LIBDIR="$(WINDOWS_OPUS_PKGCONFIG)" \
	CGO_CFLAGS="-I$(WINDOWS_OPUS_INCLUDE)" \
	CGO_LDFLAGS="-L$(WINDOWS_OPUS_LIB) -lopus -static" \
	$(GO) build -tags "$(OPUS_TAGS)" -ldflags '-extldflags "-static"' -o $(SERVER_BIN_WINDOWS) ./cmd/server
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
	CC="$(WINDOWS_CC)" PKG_CONFIG="$(WINDOWS_PKG_CONFIG)" \
	PKG_CONFIG_PATH="$(WINDOWS_OPUS_PKGCONFIG)" PKG_CONFIG_LIBDIR="$(WINDOWS_OPUS_PKGCONFIG)" \
	CGO_CFLAGS="-I$(WINDOWS_OPUS_INCLUDE)" \
	CGO_LDFLAGS="-L$(WINDOWS_OPUS_LIB) -lopus -static" \
	$(GO) build -tags "$(OPUS_TAGS)" -ldflags '-extldflags "-static"' -o $(CLIENT_BIN_WINDOWS) ./cmd/client

build-server:
	mkdir -p $(BIN_DIR)
	$(GO) build -tags "$(OPUS_TAGS)" -o $(SERVER_BIN) ./cmd/server

build-client:
	mkdir -p $(BIN_DIR)
	$(GO) build -tags "$(OPUS_TAGS)" -o $(CLIENT_BIN) ./cmd/client

run-server: build-server
	$(SERVER_BIN)

run-client: build-client
	$(CLIENT_BIN)

test:
	$(GO) test -tags "$(OPUS_TAGS)" ./...

clean:
	rm -rf $(BIN_DIR)
