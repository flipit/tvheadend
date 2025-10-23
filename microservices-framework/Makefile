.PHONY: help proto build test docker-build k8s-deploy clean

# Variables
SERVICES := dvr-service epg-service channel-service
DOCKER_REGISTRY := tvheadend
VERSION := 2.0.0

help: ## Display this help message
	@echo "Tvheadend Microservices - Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

proto: ## Generate code from protocol buffers
	@echo "Generating protobuf code..."
	@buf generate
	@echo "✓ Protobuf code generated"

build: ## Build all services
	@echo "Building services..."
	@for service in $(SERVICES); do \
		echo "Building $$service..."; \
		go build -o bin/$$service ./cmd/$$service; \
	done
	@echo "✓ All services built"

test: ## Run unit tests
	@echo "Running unit tests..."
	@go test -v -race -cover ./...
	@echo "✓ Tests passed"

test-integration: ## Run integration tests
	@echo "Running integration tests..."
	@go test -v -tags=integration ./...
	@echo "✓ Integration tests passed"

bench: ## Run benchmarks
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

lint: ## Run linters
	@echo "Running linters..."
	@golangci-lint run ./...
	@echo "✓ Linting passed"

fmt: ## Format code
	@echo "Formatting code..."
	@go fmt ./...
	@echo "✓ Code formatted"

docker-build: ## Build Docker images for all services
	@echo "Building Docker images..."
	@for service in $(SERVICES); do \
		echo "Building $$service:$(VERSION)..."; \
		docker build \
			-f deployments/docker/Dockerfile.$$service \
			-t $(DOCKER_REGISTRY)/$$service:$(VERSION) \
			-t $(DOCKER_REGISTRY)/$$service:latest \
			.; \
	done
	@echo "✓ Docker images built"

docker-push: ## Push Docker images to registry
	@echo "Pushing Docker images..."
	@for service in $(SERVICES); do \
		echo "Pushing $$service:$(VERSION)..."; \
		docker push $(DOCKER_REGISTRY)/$$service:$(VERSION); \
		docker push $(DOCKER_REGISTRY)/$$service:latest; \
	done
	@echo "✓ Docker images pushed"

docker-compose-up: ## Start services with docker-compose
	@echo "Starting services with docker-compose..."
	@docker-compose up -d
	@echo "✓ Services started"

docker-compose-down: ## Stop services with docker-compose
	@echo "Stopping services with docker-compose..."
	@docker-compose down
	@echo "✓ Services stopped"

k8s-deploy: ## Deploy to Kubernetes
	@echo "Deploying to Kubernetes..."
	@kubectl apply -f deployments/kubernetes/namespace.yaml
	@kubectl apply -f deployments/kubernetes/
	@echo "✓ Deployed to Kubernetes"

k8s-delete: ## Delete from Kubernetes
	@echo "Deleting from Kubernetes..."
	@kubectl delete -f deployments/kubernetes/
	@echo "✓ Deleted from Kubernetes"

k8s-status: ## Check Kubernetes deployment status
	@echo "Checking deployment status..."
	@kubectl get pods -n tvheadend
	@kubectl get services -n tvheadend

migrate-up: ## Run database migrations
	@echo "Running database migrations..."
	@for service in $(SERVICES); do \
		echo "Migrating $$service..."; \
		./bin/$$service migrate up; \
	done
	@echo "✓ Migrations complete"

migrate-down: ## Rollback database migrations
	@echo "Rolling back database migrations..."
	@for service in $(SERVICES); do \
		echo "Rolling back $$service..."; \
		./bin/$$service migrate down; \
	done
	@echo "✓ Rollback complete"

dev: ## Run development environment
	@echo "Starting development environment..."
	@docker-compose up -d postgres redis nats jaeger
	@echo "✓ Development environment ready"

clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@rm -rf vendor/
	@docker-compose down -v
	@echo "✓ Cleaned"

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy
	@echo "✓ Dependencies downloaded"

tools: ## Install development tools
	@echo "Installing development tools..."
	@go install github.com/bufbuild/buf/cmd/buf@latest
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "✓ Tools installed"

generate: proto ## Generate all code
	@echo "Generating code..."
	@go generate ./...
	@echo "✓ Code generation complete"

all: deps proto build test ## Run all build steps

.DEFAULT_GOAL := help
