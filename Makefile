.PHONY: build run test lint fmt

build:
	go build -o bin/typesafe ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -race

lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi
	go vet ./...

fmt:
	gofmt -w .
