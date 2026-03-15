export CGO_ENABLED=1

test:
	@echo "Running tests..."
	@CGO_ENABLED=1 go tool gotestsum -- --race --vet= --count=2 -p=4 -tags=test ./...

lint:
	go tool golangci-lint run ./...

bench:
	go test -bench=. -benchmem ./...

format:
	go fmt ./...

protos:
	@echo "🔨 Generating proto code..."
	@go tool buf generate

docker-up-comparison:
	@echo "🚀 Starting comparison services..."
	@cd tests/comparison && docker compose up -d --build

docker-down-comparison:
	@echo "🚀 Stopping comparison services..."
	@cd tests/comparison && docker compose down

docker-up-cluster:
	@echo "🚀 Starting cluster..."
	@cd cluster && docker compose up -d --build

docker-down-cluster:
	@echo "🚀 Stopping cluster..."
	@cd cluster && docker compose down

docker-up:
	@echo "🚀 Starting node..."
	@docker compose up -d --build

docker-down:
	@echo "🚀 Stopping node..."
	@docker compose down