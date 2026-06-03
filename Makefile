# Copyright 2026 Flant JSC
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

# Convenience targets for local development. CI runs the same commands so
# `make test` locally is equivalent to the CI unit-test job.
#
# Unit tests deliberately exclude ./tests/... (e2e suites that require real
# VMs/clusters/SSH). Use `make e2e` for hints on running individual suites.

UNIT_PKGS := ./internal/... ./pkg/...

.PHONY: help build vet test cover e2e clean

help: ## Print this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} \
	      /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' \
	      $(MAKEFILE_LIST)

build: ## Compile every package (incl. e2e suites for refactor-breakage check).
	go build ./...

vet: ## Run go vet on every package.
	go vet ./...

test: ## Unit tests with -race -shuffle=on (no cluster needed).
	go test -race -shuffle=on $(UNIT_PKGS)

cover: ## Unit tests + coverage profile + total %.
	go test -race -shuffle=on -covermode=atomic \
	    -coverprofile=coverage.out $(UNIT_PKGS)
	@echo "Total coverage:"
	@go tool cover -func=coverage.out | tail -1

e2e: ## Hints for running an individual e2e suite (requires VMs/cluster).
	@echo "E2E suites require real infra. Examples:"
	@echo "  go test -timeout=240m -v ./tests/test-template -count=1"
	@echo "  go test -timeout=240m -v ./tests/csi-all-stress-tests -count=1"
	@echo "See README.md for required env vars (SSH_HOST, DKP_LICENSE_KEY, ...)."

clean: ## Remove build / coverage artifacts.
	rm -f coverage.out
