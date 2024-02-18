# Targets
build: clean bins

bins: build-election-agent

all: update-tools generate clean bins test

clean: clean-bins clean-test-results

generate: proto-gen mock-gen

.PHONY: all

# Arguments
GOOS        ?= $(shell go env GOOS)
GOARCH      ?= $(shell go env GOARCH)
GOPATH      ?= $(shell go env GOPATH)
CGO_ENABLED ?= 0
LDFLAGS ?= -ldflags="-s -w"

V ?= 0
ifeq ($(V), 1)
override VERBOSE_TAG := -v
endif

# Variables
GOBIN := $(if $(shell go env GOBIN),$(shell go env GOBIN),$(GOPATH)/bin)
PATH := $(GOBIN):$(PATH)

define NEWLINE


endef

TEST_TIMEOUT := 5m

ALL_SRC         := $(shell find . -name "*.go")
ALL_SRC         += go.mod
TEST_DIRS       := $(sort $(dir $(filter %_test.go,$(ALL_SRC))))

# Code coverage output files.
COVER_ROOT                 := ./.coverage
COVER_PROFILE         := $(COVER_ROOT)/coverprofile.out
SUMMARY_COVER_PROFILE      := $(COVER_ROOT)/summary.out

# Programs
run:
	@go run ./cmd/election-agent

# Build
build-election-agent: $(ALL_SRC)
	@printf "Build election-agent with CGO_ENABLED=$(CGO_ENABLED) for $(GOOS)/$(GOARCH)...\n"
	CGO_ENABLED=$(CGO_ENABLED) go build -tags production $(LDFLAGS) -o election-agent ./cmd/election-agent

# Clean
clean-bins:
	@printf "Delete old binaries...\n"
	@rm -f election-agent

# Generate targets
mock-gen:
	@printf "Generate interface mocks...\n"
	@mockery

proto-gen:
	@protoc -I proto/ --go_out=proto/ --go-grpc_out=proto/ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative  ./proto/election_agent/v1/*.proto

# Tools
update-tools: update-protobuf update-mockery update-linter

update-protobuf:
	@printf "Install/update protobuf tools...\n"
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.32.0
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0

update-mockery:
	@printf "Install/update mockery tool...\n"
	@go install github.com/vektra/mockery/v2@v2.40.3

update-linter:
	@printf "Install/update linter tool...\n"
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.56.1

# Tests
clean-test-results:
	@rm -f test.log
	@go clean -testcache

build-tests:
	@printf "Build tests...\n"
	@go test -exec="true" -count=0 $(TEST_DIRS)

test: clean-test-results
	@printf "Run tests...\n"
	$(foreach TEST_DIR,$(TEST_DIRS),\
		@go test $(TEST_DIR) -timeout=$(TEST_TIMEOUT) $(VERBOSE_TAG) -race | tee -a test.log \
	$(NEWLINE))
	@! grep -q "^--- FAIL" test.log

##### Coverage #####
$(COVER_ROOT):
	@mkdir -p $(COVER_ROOT)

coverage: $(COVER_ROOT)
	@printf "Run unit tests with coverage...\n"
	@echo "mode: atomic" > $(COVER_PROFILE)
	$(foreach TEST_DIR,$(patsubst ./%/,%,$(TEST_DIRS)),\
		@mkdir -p $(COVER_ROOT)/$(TEST_DIR); \
		go test ./$(TEST_DIR) -timeout=$(TEST_TIMEOUT) -race -coverprofile=$(COVER_ROOT)/$(TEST_DIR)/coverprofile.out || exit 1; \
		grep -v -e "^mode: \w\+" $(COVER_ROOT)/$(TEST_DIR)/coverprofile.out >> $(COVER_PROFILE) || true \
	$(NEWLINE))

.PHONY: $(SUMMARY_COVER_PROFILE)
$(SUMMARY_COVER_PROFILE): $(COVER_ROOT)
	@printf "Combine coverage reports to $(SUMMARY_COVER_PROFILE)...\n"
	@rm -f $(SUMMARY_COVER_PROFILE)
	@echo "mode: atomic" > $(SUMMARY_COVER_PROFILE)
	$(foreach COVER_PROFILE,$(wildcard $(COVER_ROOT)/*coverprofile.out),\
		@printf "Add %s...\n" $(COVER_PROFILE); \
		grep -v -e "[Mm]ocks\?.go" -e "^mode: \w\+" $(COVER_PROFILE) >> $(SUMMARY_COVER_PROFILE) || true \
	$(NEWLINE))

coverage-report: $(SUMMARY_COVER_PROFILE)
	@printf "Generate HTML report from $(SUMMARY_COVER_PROFILE) to $(SUMMARY_COVER_PROFILE).html...\n"
	@go tool cover -html=$(SUMMARY_COVER_PROFILE) -o $(SUMMARY_COVER_PROFILE).html

# Checks
check: lint vet

lint:
	@printf "Run linter...\n"
	@golangci-lint run

# Misc
update-gomod: gomod-tidy gomod-vendor

gomod-tidy:
	@printf "go mod tidy...\n"
	@go mod tidy

gomod-vendor:
	@printf "go mod vendor...\n"
	@go mod vendor
