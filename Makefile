GO        ?= go
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GOFLAGS   := -trimpath
export CGO_ENABLED=0

.PHONY: check vet test backend build web run release clean

check: vet test backend

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

backend:
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o bin/openplexd ./cmd/openplexd

web:
	cd web && npm ci && npm run build
	find internal/ui/dist -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	cp -r web/out/. internal/ui/dist/

build: web backend

run: backend
	./bin/openplexd --data-dir ./.data --listen :7080 --dev

release: web
	mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o dist/openplexd-linux-amd64 ./cmd/openplexd
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o dist/openplexd-linux-arm64 ./cmd/openplexd
	cd dist && sha256sum openplexd-* > SHA256SUMS

clean:
	rm -rf bin dist
