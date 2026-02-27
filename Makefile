.PHONY: generate build run test test-race lint dev check clean

BINARY := shelterkin
BUILD_DIR := bin

generate:
	templ generate
	sqlc generate
	npx tailwindcss -i input.css -o static/css/styles.css --minify

build: generate
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/shelterkin

run:
	go run ./cmd/shelterkin

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	go vet ./...

dev:
	air

check: lint test test-race

clean:
	rm -rf $(BUILD_DIR) tmp
