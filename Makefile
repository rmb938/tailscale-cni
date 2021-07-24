DOCKER_ARCHS ?= amd64 armv7 arm64
BUILD_DOCKER_ARCHS = $(addprefix docker-,$(DOCKER_ARCHS))
PUSH_DOCKER_ARCHS = $(addprefix docker-push-,$(DOCKER_ARCHS))
LATEST_DOCKER_ARCHS = $(addprefix docker-latest-,$(DOCKER_ARCHS))

BUILD_GO_ARCHS = $(addprefix cni-,$(DOCKER_ARCHS))

DOCKER_IMAGE_NAME ?= tailscale-cni
DOCKER_REPO ?= local
DOCKER_IMAGE_TAG  ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))

CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: cni

# Run tests
test: generate fmt vet manifests
	go test ./... -coverprofile cover.out

.PHONY: cni $(BUILD_GO_ARCHS)
cni: fmt vet $(BUILD_GO_ARCHS)
$(BUILD_GO_ARCHS): cni-%:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(if $(filter $*,armv7),arm,$*) go build -o bin/tailscale-cni-linux-$*-raw main.go
	upx -f -o bin/tailscale-cni-linux-$*-compressed bin/tailscale-cni-linux-$*-raw
	cp bin/tailscale-cni-linux-$*-compressed bin/tailscale-cni-linux-$*

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Run tilt
tilt:
	KUBECONFIG=kubeconfig tilt up --hud=true --no-browser

# Remove tilt
tilt-down:
	KUBECONFIG=kubeconfig tilt down

.PHONY: docker $(BUILD_DOCKER_ARCHS)
docker: $(BUILD_DOCKER_ARCHS)
$(BUILD_DOCKER_ARCHS): docker-%:
	docker build -t "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$*:$(DOCKER_IMAGE_TAG)" \
		--build-arg ARCH="$*" \
		--build-arg OS="linux" \
		.

.PHONY: docker-latest $(LATEST_DOCKER_ARCHS)
docker-latest: $(LATEST_DOCKER_ARCHS)
$(LATEST_DOCKER_ARCHS): docker-latest-%:
	docker tag "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$*:$(DOCKER_IMAGE_TAG)" "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$*:latest"

.PHONY: docker-push $(PUSH_DOCKER_ARCHS)
docker-push: $(PUSH_DOCKER_ARCHS)
$(PUSH_DOCKER_ARCHS): docker-push-%:
	docker push "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$*:$(DOCKER_IMAGE_TAG)"

.PHONY: docker-manifest
docker-manifest:
	DOCKER_CLI_EXPERIMENTAL=enabled docker manifest create -a "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" $(foreach ARCH,$(DOCKER_ARCHS),$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$(ARCH):$(DOCKER_IMAGE_TAG))
	$(foreach ARCH,$(DOCKER_ARCHS),DOCKER_CLI_EXPERIMENTAL=enabled docker manifest annotate --os linux --arch $(if $(filter $(ARCH),armv7),arm,$(ARCH)) $(DOCKER_REPO)/$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG) $(DOCKER_REPO)/$(DOCKER_IMAGE_NAME)-linux-$(ARCH):$(DOCKER_IMAGE_TAG);)
	DOCKER_CLI_EXPERIMENTAL=enabled docker manifest push "$(DOCKER_REPO)/$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)"
