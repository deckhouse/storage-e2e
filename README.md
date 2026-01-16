# E2E Tests

End-to-end tests for Deckhouse storage components.

## Quick Start

1. Create test with script: `cd tests && ./create-test.sh <your-test-name>`
2. Update environment variables in `tests/<your-test-name>/test_exports`
3. Apply them: `source tests/<your-test-name>/test_exports`
4. Write your test in `tests/<your-test-name>/<your-test-name>_test.go` (Section marked `---=== TESTS START HERE ===---`)
5. Run the test: `go test -timeout=120m -v ./tests/<your-test-name> -count=1`

The `-count=1` flag prevents Go from using cached test results.
Timeout `120m` is a global timeout for entire testkit. Adjust it on your needs.

### Run a specific test inside testkit

```bash
go test -timeout=30m -v ./tests/test-folder-name -count=1 -ginkgo.focus="should create virtual machines"
```

## Testkits description

### test-template

> NOTE: DO NOT EDIT THIS TESTKIT!

Template folder for creating new E2E tests. Contains a complete framework with:
- Automatic test cluster creation and teardown
- Module enablement and readiness verification
- Environment variable validation and configuration
- Example test structure with BeforeAll/AfterAll hooks

Use `./tests/create-test.sh <your-test-name>` to create a new test from this template.

### csi-huawei-stress-tests

Stress tests for the CSI Huawei storage driver. This test suite:
- Creates a test cluster with required modules (snapshot-controller, csi-huawei)
- Configures Huawei storage resources (HuaweiStorageConnection, HuaweiStorageClass, NGCs)
- Runs flog stress test with PVC resize operations (100 pods, 100Mi → 200Mi)
- Runs comprehensive snapshot/resize/clone stress test (100 pods, multiple resize stages, snapshots, clones)

Designed to validate CSI Huawei driver stability under high load with concurrent PVC operations, snapshots, and clones.

Run the test: `go test -timeout=120m -v ./tests/csi-huawei-stress-tests -count=1`
