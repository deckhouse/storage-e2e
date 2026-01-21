# Internal Package

Low-level code that directly interacts with system components (Kubernetes, SSH, infrastructure).

## ✅ What Belongs Here

Direct, atomic operations on components:

```go
// ✅ CORRECT: Single, direct operations
GetModule(ctx, config, name)
UpdateModule(ctx, config, module)
GetModuleConfig(ctx, config, name)
```

## ❌ What Does NOT Belong Here

Business logic that orchestrates multiple operations:

```go
// ❌ INCORRECT: Combines multiple operations with logic
EnsureModuleEnabled(ctx, config, name)    // Checks + enables
CheckSnapshotControllerReady(ctx, config)  // Polls until ready
UpdateVirtualization(ctx, config, settings) // Validates + updates
```

Place these in higher-level packages `pkg/deckhouse/`, `pkg/kubernetes`, `pkg/testkit` etc.

## Rule of Thumb

**One function = one direct operation.** If it does multiple things or contains business logic, it doesn't belong here.
