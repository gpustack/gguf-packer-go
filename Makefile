.SILENT:
.DEFAULT_GOAL := ci

SHELL := /bin/bash

SRCDIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
VERSION ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null | tr '[:upper:]' '[:lower:]' || echo "unknown")

DEPS_UPDATE ?= false
deps:
	@echo "+++ $@ +++"

	cd $(SRCDIR) && go mod tidy && go mod download
	cd $(SRCDIR)/cmd/gguf-packer && go mod tidy && go mod download

	if [[ "$(DEPS_UPDATE)" == "true" ]]; then \
		cd $(SRCDIR) && go get -u -v ./...; \
		cd $(SRCDIR)/cmd/gguf-packer && go get -u -v ./...; \
	fi

	@echo "--- $@ ---"

generate:
	@echo "+++ $@ +++"

	cd $(SRCDIR) && go generate ./...
	cd $(SRCDIR)/cmd/gguf-packer && go generate ./...

	@echo "--- $@ ---"

LINT_DIRTY ?= false
lint:
	@echo "+++ $@ +++"

	if [[ "$(LINT_DIRTY)" == "true" ]]; then \
  		if [[ -n $$(git status --porcelain) ]]; then \
  			echo "Code tree is dirty."; \
  			exit 1; \
  		fi; \
	fi

	[[ -d "$(SRCDIR)/.sbin" ]] || mkdir -p "$(SRCDIR)/.sbin"

	[[ -f "$(SRCDIR)/.sbin/goimports-reviser" ]] || \
		curl --retry 3 --retry-all-errors --retry-delay 3 -sSfL "https://github.com/incu6us/goimports-reviser/releases/download/v3.6.5/goimports-reviser_3.6.5_$(GOOS)_$(GOARCH).tar.gz" \
		| tar -zxvf - --directory "$(SRCDIR)/.sbin" --no-same-owner --exclude ./LICENSE --exclude ./README.md && chmod +x "$(SRCDIR)/.sbin/goimports-reviser"
	cd $(SRCDIR) && \
		go list -f "{{.Dir}}" ./... | xargs -I {} find {} -maxdepth 1 -type f -name '*.go' ! -name 'gen.*' ! -name 'zz_generated.*' \
		| xargs -I {} "$(SRCDIR)/.sbin/goimports-reviser" -use-cache -imports-order=std,general,company,project,blanked,dotted -output=file {}
	cd $(SRCDIR)/cmd/gguf-packer && \
		go list -f "{{.Dir}}" ./... | xargs -I {} find {} -maxdepth 1 -type f -name '*.go' ! -name 'gen.*' ! -name 'zz_generated.*' \
		| xargs -I {} "$(SRCDIR)/.sbin/goimports-reviser" -use-cache -imports-order=std,general,company,project,blanked,dotted -output=file {}

	[[ -f "$(SRCDIR)/.sbin/golangci-lint" ]] || \
		curl --retry 3 --retry-all-errors --retry-delay 3 -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
		| sh -s -- -b "$(SRCDIR)/.sbin" "v1.59.0"
	cd $(SRCDIR) && \
		"$(SRCDIR)/.sbin/golangci-lint" run --fix ./...
	cd $(SRCDIR)/cmd/gguf-packer && \
		"$(SRCDIR)/.sbin/golangci-lint" run --fix ./...

	@echo "--- $@ ---"

test:
	@echo "+++ $@ +++"

	go test -v -failfast -race -cover -timeout=30m $(SRCDIR)/...

	@echo "--- $@ ---"


benchmark:
	@echo "+++ $@ +++"

	go test -v -failfast -run="^Benchmark[A-Z]+" -bench=. -benchmem -timeout=30m $(SRCDIR)/...

	@echo "--- $@ ---"

gguf-packer:
	[[ -d "$(SRCDIR)/.dist" ]] || mkdir -p "$(SRCDIR)/.dist"

	cd "$(SRCDIR)/cmd/gguf-packer" && for os in darwin linux windows; do \
  		if [[ $$os == "windows" ]]; then \
		  suffix=".exe"; \
		else \
		  suffix=""; \
		fi; \
		for arch in amd64 arm64; do \
		  	echo "Building gguf-packer for $$os-$$arch $(VERSION)"; \
			GOOS="$$os" GOARCH="$$arch" CGO_ENABLED=0 go build \
				-trimpath \
				-ldflags="-w -s -X main.Version=$(VERSION)" \
				-tags="urfave_cli_no_docs netgo" \
				-o $(SRCDIR)/.dist/gguf-packer-$$os-$$arch$$suffix; \
		done; \
		if [[ $$os == "darwin" ]]; then \
		  [[ -d "$(SRCDIR)/.sbin" ]] || mkdir -p "$(SRCDIR)/.sbin"; \
		  [[ -f "$(SRCDIR)/.sbin/lipo" ]] || \
			GOBIN="$(SRCDIR)/.sbin" go install github.com/konoui/lipo@v0.9.1; \
		  	"$(SRCDIR)/.sbin/lipo" -create -output $(SRCDIR)/.dist/gguf-packer-darwin-universal $(SRCDIR)/.dist/gguf-packer-darwin-amd64 $(SRCDIR)/.dist/gguf-packer-darwin-arm64; \
		fi;\
		if [[ $$os == "$(GOOS)" ]] && [[ $$arch == "$(GOARCH)" ]]; then \
			cp -rf $(SRCDIR)/.dist/gguf-packer-$$os-$$arch$$suffix $(SRCDIR)/.dist/gguf-packer$$suffix; \
		fi; \
	done

build: gguf-packer

PACKAGE_PUBLISH ?= false
PACKAGE_REGISTRY ?= "gpustack"
PACKAGE_IMAGE ?= "gguf-packer"
package: build
	@echo "+++ $@ +++"

	if [[ -z $$(command -v docker) ]]; then \
  		echo "Docker is not installed."; \
		exit 1; \
	fi; \
	platform="linux/amd64,linux/arm64"; \
	image="$(PACKAGE_IMAGE):$(VERSION)"; \
	if [[ -n "$(PACKAGE_REGISTRY)" ]]; then \
		image="$(PACKAGE_REGISTRY)/$$image"; \
	fi; \
	if [[ "$(PACKAGE_PUBLISH)" == "true" ]]; then \
	  	if [[ -z $$(docker buildx inspect --builder "gguf-packer") ]]; then \
      		docker run --rm --privileged tonistiigi/binfmt:qemu-v7.0.0 --install $$platform; \
      		docker buildx create --name "gguf-packer" --driver "docker-container" --buildkitd-flags "--allow-insecure-entitlement security.insecure --allow-insecure-entitlement network.host" --bootstrap; \
      	fi; \
		docker buildx build --progress=plain --platform=$$platform --sbom=true --provenance=true --builder="gguf-packer" --output="type=image,name=$$image,push=true" "$(SRCDIR)"; \
	else \
	  	platform="linux/$(GOARCH)"; \
  		docker buildx build --progress=plain --platform=$$platform --output="type=docker,name=$$image" "$(SRCDIR)"; \
	fi

	@echo "--- $@ ---"

ci: deps generate test lint build
