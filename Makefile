.PHONY: help build run up down clean test setup-env ops export-identity import-identity

# Default target
help:
	@echo "Available targets:"
	@echo "  build            - Build the API"
	@echo "  run              - Run API locally"
	@echo "  ops              - Build the operator CLI (bin/ops)"
	@echo "  export-identity  - Build ops and run export-identity"
	@echo "  import-identity  - Build ops (pass infile: make import-identity FILE=...)"
	@echo "  up               - Run service with Docker Compose"
	@echo "  down             - Stop all docker services"
	@echo "  clean            - Clean up containers and volumes"
	@echo "  test             - Run tests"
	@echo "  env              - Create .env file from env.example"

# Build targets
build:
	@echo "Building API..."
	go build .

ops:
	@echo "Building ops CLI..."
	@mkdir -p bin
	go build -tags ops -o bin/ops .

export-identity: ops
	./bin/ops export-identity

import-identity: ops
	@if [ -z "$(FILE)" ]; then echo "usage: make import-identity FILE=path/to/bundle.json.gpg"; exit 2; fi
	./bin/ops import-identity "$(FILE)"

# Run targets for development
run:
	go build .
	./syrinx


# Docker targets
up:
	@echo "Starting all services with Docker Compose..."
	docker-compose up --build

down:
	@echo "Stopping all servicema ..."
	docker-compose down

clean:
	@echo "Cleaning up containers and volumes..."
	docker-compose down -v --remove-orphans
	docker system prune -f

# Test target
test:
	@echo "Running tests..."
	go test ./...

# Install dependencies
install:
	@echo "Installing dependencies..."
	go mod download

# Setup environment
env:
	@echo "Setting up environment files..."
	@if [ ! -f .env ]; then \
		cp env.example .env; \
		echo "Created .env from env.example"; \
	fi
