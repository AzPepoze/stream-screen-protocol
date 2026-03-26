GO ?= go
BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/server
CLIENT_BIN := $(BIN_DIR)/client

.PHONY: build build-server build-client run-server run-client test clean
.PHONY: all build build-server build-client run-server run-client test clean

all: build test

build: build-server build-client

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
