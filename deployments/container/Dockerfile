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
#
# Modifications Copyright 2026 Rebellions Inc.
# This file is derived from github.com/kubernetes-sigs/dra-example-driver
# (deployments/container/Dockerfile, commit 7c9b507c) and has been
# modified by Rebellions Inc. See NOTICE for details.

ARG GOLANG_VERSION=undefined
ARG BASE_IMAGE=undefined
FROM --platform=$BUILDPLATFORM golang:${GOLANG_VERSION} AS build

# The build stage needs VERSION too, not just the label below: it is what the
# startup log record reports, and without it every
# shipped image logs an empty version.
ARG VERSION
# Set by buildx per target platform. Cross-compiling on the build host keeps the
# Go toolchain off QEMU when the image is built for arm64.
ARG TARGETARCH

WORKDIR /build
COPY . .

RUN mkdir /artifacts
# CI reference clones may point at Git objects outside the build context.
# VERSION already identifies the build, so skip Go's automatic VCS stamping.
RUN GOFLAGS=-buildvcs=false make PREFIX=/artifacts VERSION="$VERSION" GOARCH="$TARGETARCH" cmds

FROM ${BASE_IMAGE}

# Refresh runtime OpenSSL security fixes even when the Ubuntu base image lags
# behind the package archive (CVE-2026-84782: libssl3 >= 3.0.2-0ubuntu1.30).
RUN apt-get update \
    && apt-get install -y --no-install-recommends --only-upgrade libssl3 \
    && rm -rf /var/lib/apt/lists/*

LABEL io.k8s.display-name="Rebellions NPU Device Plugin"
LABEL name="Rebellions NPU Device Plugin"
LABEL vendor="rebellions.ai"
ARG VERSION="N/A"
LABEL version=${VERSION}
ARG RELEASE="N/A"
LABEL release="N/A"
LABEL summary="Rebellions NPU device plugin for Kubernetes"
LABEL description="See summary"
LABEL maintainer="Rebellions sw_devops@rebellions.ai"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.source="https://github.com/RBLN-SW/k8s-device-plugin"
LABEL org.opencontainers.image.vendor="Rebellions Inc."

COPY --from=build /artifacts/rbln-device-plugin /usr/bin/rbln-device-plugin

COPY LICENSE NOTICE THIRD_PARTY_NOTICES /licenses/

ENTRYPOINT ["/usr/bin/rbln-device-plugin"]
