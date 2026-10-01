.PHONY: build test vet run-server ping clean

build:
	go build -o bin/ ./cmd/...

test:
	go test -race ./...

vet:
	go vet ./...

run-server:
	go run ./cmd/tincan-server -listen :7777 -data ./tincan-data

ping:
	go run ./cmd/tincan ping localhost:7777

clean:
	rm -rf bin tincan-data
