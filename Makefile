test:
	go test -race ./...

lint:
	golangci-lint run

bench:
	go test -bench=. -benchmem ./...

format:
	go fmt ./...