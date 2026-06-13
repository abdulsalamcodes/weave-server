.PHONY: build run test lint clean migrate-up migrate-down

APP_NAME = weave-server
BUILD_DIR = bin
MAIN_PATH = cmd/server/main.go

build:
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME)"

run:
	@echo "Running $(APP_NAME)..."
	@go run $(MAIN_PATH)

run-hot:
	@echo "Running $(APP_NAME) with hot reload..."
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	@air

test:
	@echo "Running tests..."
	@go test ./... -v -count=1 -race -timeout 60s

test-short:
	@go test ./... -short -count=1

lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@golangci-lint run ./...

vet:
	@go vet ./...

clean:
	@rm -rf $(BUILD_DIR)
	@echo "Cleaned"

migrate-up:
	@echo "Running migrations up..."
	@go run $(MAIN_PATH) migrate up

migrate-down:
	@echo "Running migrations down..."
	@go run $(MAIN_PATH) migrate down

docker-build:
	@docker build -t $(APP_NAME) .

docker-run:
	@docker compose up -d

tidy:
	@go mod tidy
	@go mod verify

all: clean vet lint build

.DEFAULT_GOAL := build
