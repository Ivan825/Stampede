GO        ?= go
PKG       := github.com/Ivan825/Stampede
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: build test race lint fmt tidy clean generate

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/stampede ./cmd/stampede

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin dist

OAPI_CODEGEN := github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.2

generate:
	$(GO) run $(OAPI_CODEGEN) -config api/oapi-codegen.yaml api/openapi.yaml
