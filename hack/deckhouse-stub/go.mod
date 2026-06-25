// Empty stub module used only to satisfy go's module-graph resolution for
// github.com/deckhouse/deckhouse submodules that are not published standalone
// (they rely on in-tree replace directives inside the deckhouse monorepo and
// only ever appear, unused, in `go list -m all`). See replace block in /go.mod.
module github.com/deckhouse/storage-e2e/hack/deckhouse-stub

go 1.26.0
