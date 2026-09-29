# tree-sitter is a C library, so every build needs CGo and a C compiler.
export CGO_ENABLED := 1

BIN := bin/kt-vibe-lsp

.PHONY: build test install vet clean

build:
	go build -o $(BIN) .

test:
	go test -race ./...

vet:
	gofmt -l . | (! grep .) && go vet ./...

install:
	go install .

clean:
	rm -rf bin
