BINARY := kubepulse

.PHONY: build test vet fmt

build:
	go build -o $(BINARY) ./cmd/kubepulse

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .
