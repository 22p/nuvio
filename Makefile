.PHONY: build build-linux-amd64 web dev-server dev-web test vet clean docker

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o nuvio ./cmd/nuvio

build-linux-amd64: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o nuvio-linux-amd64 ./cmd/nuvio

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
