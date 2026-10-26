.PHONY: build test test-race vet run docker-up docker-down

build:
	go build -o skarb ./cmd/skarb

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/skarb serve

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
