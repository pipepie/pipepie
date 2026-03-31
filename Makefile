.PHONY: build test proto clean lint dev

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X github.com/Seinarukiro2/pipepie/cmd.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o pie .

test:
	go test ./... -short -count=1 -timeout=60s

test-all:
	go test ./... -count=1 -timeout=120s -v

proto:
	protoc --go_out=internal/protocol/pb --go_opt=paths=source_relative \
		--proto_path=proto proto/wire.proto

clean:
	rm -f pie pipepie.db pipepie.key
	rm -rf dist/

lint:
	go vet ./...

dev: build
	./pie server --domain localhost --addr :8080 --tunnel-addr :9443 --admin-token dev

# Cross-compile for common targets
release:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/pie-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/pie-linux-arm64 .
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/pie-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/pie-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/pie-windows-amd64.exe .
