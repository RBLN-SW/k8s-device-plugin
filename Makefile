# Copyright 2023 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

include $(CURDIR)/common.mk

# Keep known command targets explicit because the repo only ships a single binary.
CMDS := rbln-device-plugin
CMD_TARGETS := $(patsubst %,cmd-%, $(CMDS))

CHECK_TARGETS := vet lint
MAKE_TARGETS := binaries build build-image check vendor fmt test cmds $(CHECK_TARGETS)

TARGETS := $(MAKE_TARGETS) $(CMD_TARGETS)

.PHONY: $(TARGETS)

GOOS ?= linux
# Empty means the host architecture. The container build passes buildx's
# TARGETARCH here so arm64 binaries are cross-compiled instead of built under QEMU.
GOARCH ?=

# An empty VERSION must not overwrite main.version's "dev" default with "":
# the startup log record names the running build, and "" names nothing.
ifneq ($(VERSION),)
VERSION_LDFLAGS := -X main.version=$(VERSION)
endif

binaries: cmds
ifneq ($(PREFIX),)
cmd-%: COMMAND_BUILD_OPTIONS = -o $(PREFIX)/$(*)
endif
cmds: $(CMD_TARGETS)
$(CMD_TARGETS): cmd-%:
	CGO_LDFLAGS_ALLOW='-Wl,--unresolved-symbols=ignore-in-object-files' GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "-s -w $(VERSION_LDFLAGS)" $(COMMAND_BUILD_OPTIONS) $(MODULE)/cmd/$(*)

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build ./...

# Entry point of the cloud-component-release-kit rc build:
#   make build-image IMAGE_NAME=<registry>/k8s-device-plugin VERSION=<tag> \
#        BUILD_MULTI_PLATFORM=true PUSH_ON_BUILD=true
# Command-line variables reach the sub-make through MAKEFLAGS.
build-image:
	$(MAKE) -f $(CURDIR)/deployments/container/Makefile build

all: check test binaries
check: $(CHECK_TARGETS)

# Update the vendor folder
vendor:
	go mod vendor

# Apply go fmt to the codebase
fmt:
	go list -f '{{.Dir}}' $(MODULE)/... \
		| xargs gofmt -s -l -w

lint:
	golangci-lint run ./...

vet:
	go vet $(MODULE)/...


COVERAGE_FILE := coverage.out
test: build cmds
	go test -v -coverprofile=$(COVERAGE_FILE) $(MODULE)/...
