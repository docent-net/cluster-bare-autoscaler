# Project settings
IMAGE_NAME=docent/cluster-bare-autoscaler
TAG ?= $(shell git describe --tags --always --dirty)
PLATFORMS=linux/amd64,linux/arm64

# Binary name
BIN_NAME=cluster-bare-autoscaler

# Default: run tests
.PHONY: all
all: test

.PHONY: test
test:
	go test ./...

.PHONY: set_helm_app_version
set_helm_app_version:
	sed -i 's/appVersion: .*/appVersion: $(TAG)/' helm/Chart.yaml

.PHONY: set_helm_values_tag
set_helm_values_tag:
	sed -i 's/tag: .*/tag: "$(TAG)"/' helm//values.yaml

.PHONY: update_helm_metadata
update_helm_metadata: set_helm_app_version set_helm_values_tag

.PHONY: lint
lint:
	go vet ./...

.PHONY: build_binary
build_binary:
	go build -ldflags="-X main.version=$(TAG)" -o $(BIN_NAME) ./main.go

.PHONY: build_image
build_image: build_binary
	docker build --build-arg VERSION=$(TAG) -t $(IMAGE_NAME):$(TAG) .


.PHONY: build_and_publish_image
build_and_publish_image:
	KO_DOCKER_REPO=$(IMAGE_NAME) ko publish --tags=$(TAG) --platform=linux/amd64,linux/arm64 --sbom=none

.PHONY: clean
clean:
	go clean
	rm -f $(BIN_NAME)
