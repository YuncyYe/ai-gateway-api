# Copyright(c) 2024 The Rainway AI Gateway (壬远AI网关) Authors. All rights reserved.
# Copyright (c) 2019 The BFE Authors.
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

# init project path
HOMEDIR := $(shell pwd)
OUTDIR  := $(HOMEDIR)/output


# -----------------------------------------------------------------------------
# Container image build variables (used by docker/docker-push targets)
# -----------------------------------------------------------------------------

# Image name and version
BFE_IMAGE_NAME ?= ai-gateway-api
BFE_VERSION ?= $(shell sed -nE 's/^const Version = "([^"]+)"/\1/p' version/version.go)
VERSION_TAG := v$(BFE_VERSION)

# Registry and build settings
REGISTRY ?=
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER_NAME ?= ai-gateway-api-builder
NO_CACHE ?= false
DASHBOARD_VERSION ?= v0.0.3
DOCKER_BUILD_ARGS ?=

# Derived tags
IMAGE_LOCAL := $(BFE_IMAGE_NAME):$(VERSION_TAG)
IMAGE_LATEST_LOCAL := $(BFE_IMAGE_NAME):latest
IMAGE_REMOTE := $(if $(REGISTRY),$(REGISTRY)/$(BFE_IMAGE_NAME):$(VERSION_TAG),)
IMAGE_LATEST_REMOTE := $(if $(REGISTRY),$(REGISTRY)/$(BFE_IMAGE_NAME):latest,)
# -----------------------------------------------------------------------------

# init command params
GO      := $(GO_1_16_BIN)go
GOPATH  := $(shell $(GO) env GOPATH)
GOMOD   := $(GO) mod
GOBUILD := $(GO) build
GOTEST  := $(GO) test -gcflags="-N -l"
GOPKGS  := $$($(GO) list ./...| grep -vE "vendor")
LICENSEEYE   := license-eye

# test cover files
COVPROF := $(HOMEDIR)/covprof.out  # coverage profile
COVFUNC := $(HOMEDIR)/covfunc.txt  # coverage profile information for each function
COVHTML := $(HOMEDIR)/covhtml.html # HTML representation of coverage profile

# make, make all
all: prepare compile package

#make prepare, download dependencies
prepare: gomod

gomod:
	$(GOMOD) download

#make compile
compile: build

build: prepare
	$(GOBUILD) -o $(HOMEDIR)/ai-gateway-api

# make test, test your code
test: prepare test-case
test-case:
	$(GOTEST) -v -cover $(GOPKGS)

# model-only unit tests
MODEL_COVERAGE_THRESHOLD := 70

.PHONY: test-model test-model-cover test-model-cover-gate
test-model:
	$(GOTEST) -v ./model/...

test-model-cover:
	$(GOTEST) -cover ./model/...

test-model-cover-gate:
	@echo "Running model tests with coverage gate ($(MODEL_COVERAGE_THRESHOLD)%)..."
	@$(GOTEST) -coverprofile=$(HOMEDIR)/model_coverage.out ./model/... >/dev/null && \
	COV=$$(go tool cover -func=$(HOMEDIR)/model_coverage.out | grep '^total:' | awk '{print $$3}' | sed 's/%//') && \
	echo "Model coverage: $$COV%" && \
	if awk -v cov="$$COV" -v threshold="$(MODEL_COVERAGE_THRESHOLD)" 'BEGIN {exit (cov+0 >= threshold+0) ? 0 : 1}'; then \
		echo "Coverage gate passed"; \
	else \
		echo "Model coverage $$COV% is below threshold $(MODEL_COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi

# make package
package: package-bin
package-bin:
	mkdir -p 			$(OUTDIR)
	cp -rf  conf 		$(OUTDIR)/
	cp -rf  static 		$(OUTDIR)/
	cp -rf  docs 		$(OUTDIR)/
	cp -rf  scripts 	$(OUTDIR)/
	cp -rf  database 	$(OUTDIR)/
	cp -rf  db_ddl.sql 	$(OUTDIR)/
	cp -rf  db_ddl_sqlite.sql 	$(OUTDIR)/
	cp -rf  db_ddl_report_mysql.sql 	$(OUTDIR)/
	cp -rf  *.md 		$(OUTDIR)/
	mv ai-gateway-api  		$(OUTDIR)/


# make docker
docker:
	docker build $(if $(filter 1 true TRUE True,$(NO_CACHE)),--no-cache,) $(DOCKER_BUILD_ARGS) \
		--build-arg DASHBOARD_VERSION=$(DASHBOARD_VERSION) \
		-t $(IMAGE_LOCAL) \
		-t $(IMAGE_LATEST_LOCAL) \
		.

# make docker-push (multi-arch)
docker-push:
	@test -n "$(REGISTRY)" || (echo "REGISTRY is required, e.g. REGISTRY=ghcr.io/your-org" && exit 1)
	@docker buildx inspect $(BUILDER_NAME) >/dev/null 2>&1 || docker buildx create --name $(BUILDER_NAME) --driver docker-container --use
	@docker buildx use $(BUILDER_NAME)
	@docker buildx inspect --bootstrap >/dev/null
	docker buildx build $(if $(filter 1 true TRUE True,$(NO_CACHE)),--no-cache,) $(DOCKER_BUILD_ARGS) \
		--build-arg DASHBOARD_VERSION=$(DASHBOARD_VERSION) \
		--platform $(PLATFORMS) \
		--push \
		-t $(IMAGE_REMOTE) \
		-t $(IMAGE_LATEST_REMOTE) \
		.

# make license-eye-install
license-eye-install:
	$(GO) install github.com/apache/skywalking-eyes/cmd/license-eye@latest

# make license-check, check code file's license declaration
license-check: license-eye-install
	$(LICENSEEYE) header check

# make license-fix, fix code file's license declaration
license-fix: license-eye-install
	$(LICENSEEYE) header fix

# make clean
clean:
	$(GO) clean
	rm -rf $(OUTDIR)
	rm -rf $(HOMEDIR)/ai-gateway-api
	rm -rf $(GOPATH)/pkg/darwin_amd64

# avoid filename conflict and speed up build 
.PHONY: all prepare compile test package clean build test-model test-model-cover test-model-cover-gate