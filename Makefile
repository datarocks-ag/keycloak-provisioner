.PHONY: build test test-integration lint vet docker clean

BINARY := keycloak-provisioner

build:
	go build -o $(BINARY) ./cmd/keycloak-provisioner

test:
	go test -race ./...

test-integration:
	go test -race -tags=integration -v ./...

lint:
	golangci-lint run

vet:
	go vet ./...

docker:
	docker build -t $(BINARY) .

clean:
	rm -f $(BINARY)
