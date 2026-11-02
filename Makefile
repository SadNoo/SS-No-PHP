VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test lint run dev-up dev-down

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/panel ./cmd/panel

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

run: build
	./bin/panel -config config.toml

dev-up:
	./deploy/dev/gen-certs.sh && docker compose -f deploy/dev/docker-compose.yml up -d

dev-down:
	docker compose -f deploy/dev/docker-compose.yml down
