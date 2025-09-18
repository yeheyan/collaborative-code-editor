# Makefile for Collaborative Code Editor

# Variables
BACKEND_DIR = backend
FRONTEND_DIR = frontend
DOCKER_COMPOSE = docker-compose
GO = go
NPM = npm

# Go build variables
EDITOR_SERVICE = ./backend/cmd/editor-service
SESSION_SERVICE = ./backend/cmd/session-service
EXECUTION_SERVICE = ./backend/cmd/execution-service

# Binary output directory
BIN_DIR = bin

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ==================== Development ====================

.PHONY: dev
dev: ## Run all services in development mode
	@echo "Starting development environment..."
	$(MAKE) -j3 dev-editor dev-frontend dev-db

.PHONY: dev-editor
dev-editor: ## Run editor service in development
	@echo "Starting editor service..."
	cd $(BACKEND_DIR) && go run cmd/editor-service/main.go -env=dev

.PHONY: dev-session
dev-session: ## Run session service in development
	@echo "Starting session service..."
	cd $(BACKEND_DIR) && go run cmd/session-service/main.go -env=dev

.PHONY: dev-execution
dev-execution: ## Run execution service in development
	@echo "Starting execution service..."
	cd $(BACKEND_DIR) && go run cmd/execution-service/main.go -env=dev

.PHONY: dev-frontend
dev-frontend: ## Run frontend in development
	@echo "Starting frontend..."
	cd $(FRONTEND_DIR) && npm run dev

.PHONY: dev-db
dev-db: ## Start development databases (PostgreSQL & Redis)
	@echo "Starting databases..."
	docker-compose -f infrastructure/docker/docker-compose.dev.yml up -d postgres redis

# ==================== Build ====================

.PHONY: build
build: build-backend build-frontend ## Build all services

.PHONY: build-backend
build-backend: ## Build all backend services
	@echo "Building backend services..."
	@mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && \
		CGO_ENABLED=0 GOOS=linux go build -o ../$(BIN_DIR)/editor-service cmd/editor-service/main.go && \
		CGO_ENABLED=0 GOOS=linux go build -o ../$(BIN_DIR)/session-service cmd/session-service/main.go && \
		CGO_ENABLED=0 GOOS=linux go build -o ../$(BIN_DIR)/execution-service cmd/execution-service/main.go
	@echo "✅ Backend services built successfully"

.PHONY: build-frontend
build-frontend: ## Build frontend
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm run build
	@echo "✅ Frontend built successfully"

# ==================== Docker ====================

.PHONY: docker-build
docker-build: ## Build all Docker images
	@echo "Building Docker images..."
	docker build -f infrastructure/docker/Dockerfile.editor -t collab-editor:latest .
	docker build -f infrastructure/docker/Dockerfile.frontend -t collab-frontend:latest .
	@echo "✅ Docker images built"

.PHONY: docker-up
docker-up: ## Start all services with Docker Compose
	@echo "Starting services with Docker Compose..."
	cd infrastructure/docker && docker-compose up -d

.PHONY: docker-down
docker-down: ## Stop all Docker services
	@echo "Stopping Docker services..."
	cd infrastructure/docker && docker-compose down

.PHONY: docker-logs
docker-logs: ## Show logs from all Docker services
	cd infrastructure/docker && docker-compose logs -f

# ==================== Testing ====================

.PHONY: test
test: test-unit test-integration ## Run all tests

.PHONY: test-unit
test-unit: ## Run unit tests
	@echo "Running unit tests..."
	cd $(BACKEND_DIR) && go test -v -cover ./...

.PHONY: test-integration
test-integration: ## Run integration tests
	@echo "Running integration tests..."
	cd tests/integration && go test -v ./...

.PHONY: test-load
test-load: ## Run load tests with k6
	@echo "Running load tests..."
	k6 run tests/load/websocket-test.js

.PHONY: test-coverage
test-coverage: ## Generate test coverage report
	@echo "Generating coverage report..."
	cd $(BACKEND_DIR) && go test -coverprofile=coverage.out ./...
	cd $(BACKEND_DIR) && go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: backend/coverage.html"

# ==================== Database ====================

.PHONY: migrate-up
migrate-up: ## Run database migrations
	@echo "Running migrations..."
	migrate -path backend/migrations -database postgres://user:pass@localhost/collab_editor up

.PHONY: migrate-down
migrate-down: ## Rollback database migrations
	@echo "Rolling back migrations..."
	migrate -path backend/migrations -database postgres://user:pass@localhost/collab_editor down

# ==================== Utilities ====================

.PHONY: install-deps
install-deps: ## Install all dependencies
	@echo "Installing Go dependencies..."
	cd $(BACKEND_DIR) && go mod download
	@echo "Installing Node dependencies..."
	cd $(FRONTEND_DIR) && npm install
	@echo "✅ Dependencies installed"

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting Go code..."
	cd $(BACKEND_DIR) && go fmt ./...
	@echo "Formatting TypeScript code..."
	cd $(FRONTEND_DIR) && npm run format

.PHONY: lint
lint: ## Run linters
	@echo "Linting Go code..."
	cd $(BACKEND_DIR) && golangci-lint run
	@echo "Linting TypeScript code..."
	cd $(FRONTEND_DIR) && npm run lint

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	rm -rf $(FRONTEND_DIR)/dist
	rm -rf $(BACKEND_DIR)/coverage.*
	@echo "✅ Cleaned"

.PHONY: setup
setup: ## Initial project setup
	@echo "Setting up project..."
	$(MAKE) install-deps
	@echo "Creating .env file..."
	cp .env.example .env
	@echo "✅ Project setup complete"

# ==================== Monitoring ====================

.PHONY: logs
logs: ## Tail logs from all services
	@echo "Tailing logs..."
	tail -f logs/*.log

.PHONY: monitor
monitor: ## Open monitoring dashboards
	@echo "Opening monitoring dashboards..."
	open http://localhost:3000  # Grafana
	open http://localhost:9090  # Prometheus
	open http://localhost:16686 # Jaeger

# Default target
.DEFAULT_GOAL := help