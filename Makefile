.PHONY: build build-linux-amd64 web dev-server dev-web test vet clean docker

VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -s -w -X 'nuvio/internal/jellyfin.Version=$(VERSION)'

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o nuvio ./cmd/nuvio

build-linux-amd64: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o nuvio-linux-amd64 ./cmd/nuvio

web:
	cd web/ui && npm ci && npm run build

dev-server:
	go run ./cmd/nuvio

dev-web:
	cd web/ui && npm run dev

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f nuvio
	rm -rf web/dist

docker:
	docker build -t nuvio .
