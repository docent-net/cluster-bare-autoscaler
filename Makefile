# Project settings
IMAGE_NAME=docent/cluster-bare-autoscaler
TAG ?= $(shell git describe --tags --always --dirty)
PLATFORMS=linux/amd64,linux/arm64
GO               ?= go
PKG              ?= ./...
INTEGRATION_PKG  ?= ./test/integration/...

COVER_DIR   := coverage
UNIT_PROFILE := $(COVER_DIR)/unit.out
INT_PROFILE  := $(COVER_DIR)/integration.out

# Binary name
BIN_NAME=cluster-bare-autoscaler

# Default: run tests
.PHONY: all
all: unit

.PHONY: unit
unit:
	@mkdir -p $(COVER_DIR)
	$(GO) test -race -covermode=atomic -coverprofile=$(UNIT_PROFILE) $(PKG)

.PHONY: test-integration
test-integration:
	@mkdir -p $(COVER_DIR)
	@if [ -d test/integration ]; then \
	  $(GO) test -race -tags=integration -covermode=atomic -coverpkg=./... -coverprofile=$(INT_PROFILE) $(INTEGRATION_PKG); \
	else \
	  echo "no integration tests found under test/integration — skipping"; \
	fi

.PHONY: test-all
test-all: fmt vet unit test-integration

.PHONY: helm-checks
helm-checks:
	./tools/helm-checks/run.sh

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
	$(GO) vet ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: build_binary
build_binary:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	$(GO) build -ldflags="-s -w -X main.version=$(TAG)" \
	-o bin/$(BIN_NAME) ./main.go

.PHONY: build_image
build_image: build_binary
	docker build --build-arg VERSION=$(TAG) -t $(IMAGE_NAME):$(TAG) .


.PHONY: build_and_publish_image
build_and_publish_image:
	KO_DOCKER_REPO=$(IMAGE_NAME) ko publish --tags=$(TAG) --bare

.PHONY: clean
clean:
	$(GO) clean
	rm -f bin/$(BIN_NAME)
	rm -rf $(COVER_DIR)
