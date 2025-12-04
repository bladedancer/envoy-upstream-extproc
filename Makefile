GIT_VERSION ?= $(shell git describe --abbrev=8 --tags --always --dirty)
IMAGE_PREFIX ?= bladedancer
SERVICE_NAME=extprocdemo

.PHONY: default
default: local.build ;

.PHONY: clean
clean:
	go clean
	rm -f bin/${SERVICE_NAME}

.PHONY: local.build
local.build: clean
	GOARCH=amd64 GOOS=linux go build -o bin/${SERVICE_NAME} main.go

.PHONY: local.test
local.test:
	go test -v ./...

.PHONY: local.test.coverage
local.test.coverage:
	go test ./... -coverprofile=coverage.out

.PHONY: local.run
local.run:
	go run ./main.go --port 10001

.PHONY: docker.build
docker.build:
	# image gets tagged as latest by default
	docker build -t $(IMAGE_PREFIX)/$(SERVICE_NAME) -f ./Dockerfile .
	# tag with git version as well
	docker tag $(IMAGE_PREFIX)/$(SERVICE_NAME) $(IMAGE_PREFIX)/$(SERVICE_NAME):$(GIT_VERSION)

.PHONY: docker.run
docker.run:  # defaults to latest tag
	docker run -p 10001:10001 $(IMAGE_PREFIX)/$(SERVICE_NAME)

.PHONY: docker.push
docker.push: docker.build
	docker push $(IMAGE_PREFIX)/$(SERVICE_NAME):$(GIT_VERSION)
	docker push $(IMAGE_PREFIX)/$(SERVICE_NAME):latest

dep:
	go mod tidy

vet:
	go vet

lint:
	golangci-lint run --enable-all