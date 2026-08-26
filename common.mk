# Copyright 2022 The Kubernetes Authors.
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

# Keep in sync with go.mod's toolchain directive: this is the Go that builds the
# shipped binary, so its stdlib is what the image scan reports.
GOLANG_VERSION ?= 1.26.7

# Read by .github/workflows/image-scan.yaml so CI and `make scan-image` agree.
TRIVY_VERSION ?= v0.74.0

MODULE := github.com/RBLN-SW/k8s-device-plugin

VERSION  ?=
vVERSION := v$(VERSION:v%=%)

ifeq ($(IMAGE_NAME),)
REGISTRY ?= harbor.k8s.rebellions.in/rebellions
IMAGE_NAME = $(REGISTRY)/k8s-device-plugin
endif
