VERSION ?= dev
LDFLAGS := -s -w -X mtun/internal/version.Version=$(VERSION)

.PHONY: build build-linux build-windows tidy run-server run-client

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtun.exe ./cmd/mtun

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtun-linux-amd64 ./cmd/mtun

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtun-windows-amd64.exe ./cmd/mtun

tidy:
	go mod tidy

run-server:
	go run ./cmd/mtun server -c configs/server.example.yaml

run-client:
	go run ./cmd/mtun client -c configs/client.example.yaml
