.PHONY: all build test vet race lint clean tidy bench cover

PKG := ./...
BIN_DIR := bin

all: vet test build

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/vctl ./cmd/vctl

test:
	go test -count=1 $(PKG)

race:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

tidy:
	go mod tidy

cover:
	go test -coverprofile=coverage.txt -covermode=atomic $(PKG)
	go tool cover -func=coverage.txt | tail -1

clean:
	rm -rf $(BIN_DIR) coverage.txt
