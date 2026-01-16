# E2E Tests

End-to-end tests for Deckhouse storage components.

## Quick Start

1. Create test with script: `cd tests && ./create-test.sh <your-test-name>`
2. Update environment variables in `tests/<your-test-name>/test_exports`
3. Apply them: `source tests/<your-test-name>/test_exports`
4. Write your test in `tests/<your-test-name>/<your-test-name>_test.go` (Section marked `---=== TESTS START HERE ===---`)
5. Run the test: `go test -timeout=60m -v ./tests/<your-test-name> -count=1`

The `-count=1` flag prevents Go from using cached test results.
Timeout `60m` is a global timeout for entire testkit. Adjust it on your needs.

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




