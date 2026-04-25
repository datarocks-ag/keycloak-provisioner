.PHONY: build test test-integration lint vet fmt mod-tidy cover docker clean

BINARY := keycloak-provisioner

build:
	go build -o $(BINARY) ./cmd/keycloak-provisioner

test:
	go test -race ./...

test-integration:
	go test -race -tags=integration -v ./...

lint:
	go tool golangci-lint run

vet:
	go vet ./...

fmt:
	go fmt ./...

mod-tidy:
	go mod tidy

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

docker:
	docker build -t $(BINARY) .

clean:
	rm -f $(BINARY) coverage.out
