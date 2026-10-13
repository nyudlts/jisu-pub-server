help:
	@echo "Usage: make <target>\n\n\
	  build\t\tBuild the \`rwp\` command-line utility in the current directory\n\
	  install\tBuild and install the \`rwp\` command-line utility\n\
					verify-local-ci\tRun local checks mirroring the manual CI workflow\n\
					verify-storage-conformance\tRun storage backend conformance test targets\n\
					verify-integration-local\tRun integration tests in local storage mode\n\
	"

.PHONY: build
build:
	(cd cmd/rwp; go build; mv rwp ../..)

.PHONY: install
install:
	(cd cmd/rwp; go install)

.PHONY: verify-storage-conformance
verify-storage-conformance:
	go test -v ./pkg/storage -run 'TestLocalBackendConformance'
	go test -v ./pkg/storage -run 'TestHTTPBackendConformance'
	go test -v ./pkg/storage -run 'TestS3BackendConformance'

.PHONY: verify-integration-local
verify-integration-local:
	STORAGE=local go test -v -tags=integration ./integration/...

.PHONY: verify-local-ci
verify-local-ci:
	go build -v ./...
	go vet ./...
	go test -v ./...
	$(MAKE) verify-storage-conformance
	$(MAKE) verify-integration-local


