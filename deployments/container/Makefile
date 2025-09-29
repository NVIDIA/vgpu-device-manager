# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
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

BUILD_MULTI_ARCH_IMAGES ?= no
DOCKER ?= docker
BUILDX =
ifeq ($(BUILD_MULTI_ARCH_IMAGES),true)
BUILDX = buildx
endif

##### Global variables #####
include $(CURDIR)/versions.mk

ifeq ($(IMAGE_NAME),)
REGISTRY ?= nvidia
IMAGE_NAME := $(REGISTRY)/vgpu-device-manager
endif

GOPROXY ?= direct

IMAGE_VERSION := $(VERSION)
IMAGE = $(IMAGE_NAME):$(IMAGE_VERSION)

OUT_IMAGE_NAME ?= $(IMAGE_NAME)
OUT_IMAGE_VERSION ?= $(IMAGE_VERSION)
OUT_IMAGE = $(OUT_IMAGE_NAME):$(OUT_IMAGE_VERSION)

##### Public rules #####
DISTRIBUTIONS := ubi9
DEFAULT_PUSH_TARGET := ubi9

PUSH_TARGETS := $(patsubst %, push-%, $(DISTRIBUTIONS))
BUILD_TARGETS := $(patsubst %, build-%, $(DISTRIBUTIONS))
TEST_TARGETS := $(patsubst %, build-%, $(DISTRIBUTIONS))

.PHONY: $(DISTRIBUTIONS) $(PUSH_TARGETS) $(BUILD_TARGETS) $(TEST_TARGETS)

ifneq ($(BUILD_MULTI_ARCH_IMAGES),true)
include $(CURDIR)/deployments/container/native-only.mk
else
include $(CURDIR)/deployments/container/multi-arch.mk
endif

push-%: DIST = $(*)


build-%: DIST = $(*)
build-%: DOCKERFILE_SUFFIX = $(*)
build-%: DOCKERFILE = $(CURDIR)/deployments/container/Dockerfile.$(DOCKERFILE_SUFFIX)

# Both ubi9 and build-ubi9 trigger a build of the relevant image
$(DISTRIBUTIONS): %: build-%
$(BUILD_TARGETS): build-%:
	DOCKER_BUILDKIT=1 \
		$(DOCKER) $(BUILDX) build --pull \
			$(DOCKER_BUILD_OPTIONS) \
			$(DOCKER_BUILD_PLATFORM_OPTIONS) \
			--tag $(IMAGE) \
			--build-arg GOLANG_VERSION="$(GOLANG_VERSION)" \
			--build-arg VERSION="$(VERSION)" \
			--build-arg GIT_COMMIT="$(GIT_COMMIT)" \
			--build-arg CVE_UPDATES="$(CVE_UPDATES)" \
			--build-arg GOPROXY="$(GOPROXY)" \
			--file $(DOCKERFILE) \
			$(CURDIR)
