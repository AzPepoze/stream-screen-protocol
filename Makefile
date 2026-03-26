GO ?= go
BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/server
CLIENT_BIN := $(BIN_DIR)/client
SERVER_BIN_LINUX := $(BIN_DIR)/server-linux
CLIENT_BIN_LINUX := $(BIN_DIR)/client-linux
SERVER_BIN_WINDOWS := $(BIN_DIR)/server-windows.exe
CLIENT_BIN_WINDOWS := $(BIN_DIR)/client-windows.exe

.PHONY: build build-server build-client run-server run-client test clean
.PHONY: all build build-server build-client run-server run-client test clean
.PHONY: build-linux build-windows build\:linux build\:windows

all: build test

build: build-server build-client

build-linux:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -o $(SERVER_BIN_LINUX) ./cmd/server
	GOOS=linux GOARCH=amd64 $(GO) build -o $(CLIENT_BIN_LINUX) ./cmd/client

build-windows:
	mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(SERVER_BIN_WINDOWS) ./cmd/server
	GOOS=windows GOARCH=amd64 $(GO) build -o $(CLIENT_BIN_WINDOWS) ./cmd/client

build-server:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(SERVER_BIN) ./cmd/server

build-client:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(CLIENT_BIN) ./cmd/client

run-server: build-server
	$(SERVER_BIN)

run-client: build-client
	$(CLIENT_BIN)

test:
	$(GO) test ./...

clean:
	rm -rf $(BIN_DIR)
