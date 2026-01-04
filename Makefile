export CGO_ENABLED=1

test:
	go test ./...

lint:
	golangci-lint run

bench:
	go test -bench=. -benchmem ./...

format:
	go fmt ./...



## Windows-compatible protoc generator
#PROTOC = protoc
#
## Абсолютно все .proto файлы
#PROTO_FILES := $(shell dir /S /B *.proto)
#CURDIR_WIN := $(shell cd)
#REL_PROTO_FILES := $(foreach f,$(PROTO_FILES),$(subst $(CURDIR_WIN)\,,$(f)))

.PHONY: proto-gen-win
proto-gen-win:
	@echo === Generating Go files from all .proto files according to go_package ===
	@for %%f in ($(REL_PROTO_FILES)) do ( \
		echo Processing %%f & \
		$(PROTOC) -I=. --go_out=. --go-grpc_out=. %%f \
	)
	@echo === Done ===