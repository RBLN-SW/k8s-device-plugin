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

##### Global variables #####
MKDIR ?= mkdir
DISTRIBUTIONS := ubuntu22.04
DOCKERFILE := $(CURDIR)/deployments/container/Dockerfile
PUSH_ON_BUILD ?= false
BUILD_MULTI_PLATFORM ?= false

# PLATFORM selects a single target platform (e.g. linux/amd64, linux/arm64).
# If unset, BUILD_MULTI_PLATFORM=true builds linux/amd64 + linux/arm64;
# otherwise the build targets linux/amd64 (cluster NPU nodes are x86_64).
PLATFORM ?=
ifneq ($(PLATFORM),)
PLATFORM_OPTION = --platform=$(PLATFORM)
else ifeq ($(BUILD_MULTI_PLATFORM),true)
PLATFORM_OPTION = --platform=linux/amd64,linux/arm64
else
PLATFORM_OPTION = --platform=linux/amd64
endif

ifeq ($(PUSH_ON_BUILD),true)
OUTPUT_OPTION = --push
else ifeq ($(BUILD_MULTI_PLATFORM),true)
# Multi-platform images cannot be loaded into the classic docker image store.
OUTPUT_OPTION = --output=type=image,push=false
else
OUTPUT_OPTION = --load
endif

include $(CURDIR)/common.mk

IMAGE_TAG := $(VERSION)
IMAGE = $(IMAGE_NAME):$(IMAGE_TAG)

##### Public rules #####
.PHONY: $(DISTRIBUTIONS) build scan-image

build: ubuntu22.04

ubuntu22.04: BASE_IMAGE = docker.io/ubuntu:22.04

# Use a generic build target to build the relevant images
$(DISTRIBUTIONS):
	DOCKER_BUILDKIT=1 \
		docker buildx build --pull \
		$(OUTPUT_OPTION) \
		$(PLATFORM_OPTION) \
		--tag $(IMAGE) \
		--build-arg GOLANG_VERSION="$(GOLANG_VERSION)" \
		--build-arg BASE_IMAGE="$(BASE_IMAGE)" \
		--build-arg VERSION="$(VERSION)" \
		-f $(DOCKERFILE) \
		$(CURDIR)

# Same filter as .github/workflows/image-scan.yaml, so a green run here means a
# green gate on the PR.
scan-image: build
	@command -v trivy >/dev/null || { \
		echo "trivy not found: https://trivy.dev/latest/getting-started/installation/"; \
		exit 1; \
	}
	trivy image --scanners vuln,secret --severity HIGH,CRITICAL --ignore-unfixed \
		--ignorefile $(CURDIR)/.trivyignore.yaml --exit-code 1 $(IMAGE)

