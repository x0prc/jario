.PHONY: build test vet lint clean

BINARY := jario

build:
	go build -o $(BINARY) ./cmd/server

test:
	go test ./... -count=1 -timeout 30s

test-race:
	go test ./... -count=1 -race -timeout 60s

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
