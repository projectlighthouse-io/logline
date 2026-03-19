VERSION  := $(shell cat VERSION)
COMMIT   := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.buildTime=$(BUILD_TIME)

BINARY := logline
IMAGE  := logline

.PHONY: build test lint docker clean deploy

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/logline

test:
	go test -p 1 ./... -count=1

lint:
	golangci-lint run ./...

docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(IMAGE):$(VERSION) \
		-t $(IMAGE):latest \
		.

clean:
	rm -f $(BINARY)

deploy: build
	scp $(BINARY) deploy@server:/opt/logline/$(BINARY)
	ssh deploy@server 'sudo systemctl restart logline'
