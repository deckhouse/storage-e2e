# Logger Package

A structured logging package built on Go's `log/slog` that supports dual output (console + file) with configurable log levels.

## Features

- ✅ **Dual Output**: Logs to both console (human-readable) and file (structured JSON)
- ✅ **Log Levels**: Debug, Info, Warn, Error with filtering
- ✅ **Emoji Support**: Visual indicators for different log types (configurable)
- ✅ **Colored Output**: ANSI colors for better readability (console only)
- ✅ **Structured Logging**: Fields and context support via `slog`
- ✅ **Zero External Dependencies**: Built on standard library

## Usage

### Initialization

```go
import "github.com/deckhouse/storage-e2e/internal/logger"

func main() {
    // Initialize logger (reads configuration from environment variables)
    if err := logger.Initialize(); err != nil {
        panic(err)
    }
    defer logger.Close()
}
```

### Configuration

The logger is configured via environment variables:

**`LOG_LEVEL`** - Controls log verbosity:
- `debug` - Show all messages (debug, success, info, progress, skip, delete, steps, warnings, errors)
- `info` - Show major steps, info, warnings, and errors only (default) - cleaner output for production
- `warn` - Show warnings and errors only
- `error` - Show errors only

**`LOG_FILE_PATH`** - Controls file logging:
- Not set or empty: Logs to console only
- Set to a file path: Logs to both console and the specified file

**`USE_EMOJIS`** - Controls emoji display in log messages:
- `true` - Show emojis in log messages (default)
- `false` - Disable emojis for cleaner output

Examples:
```bash
# Console only
export LOG_LEVEL=info

# Console + file
export LOG_LEVEL=debug
export LOG_FILE_PATH=/tmp/e2e-tests/test.log

# Disable emojis
export USE_EMOJIS=false

# Or with timestamp
export LOG_FILE_PATH=/tmp/e2e-tests/test-$(date +%Y%m%d-%H%M%S).log
```

### Helper Functions

The package provides convenient helper functions that match the existing `fmt.Printf` style:

```go
// Major workflow steps
logger.Step(1, "Loading configuration from %s", filename)
logger.StepComplete(1, "Configuration loaded successfully")

// Status messages
logger.Success("VM created successfully")
logger.Info("Waiting for pod to be ready")
logger.Warn("Resource already exists, skipping")
logger.Error("Failed to connect: %v", err)

// Progress indicators
logger.Progress("Waiting for VM %d/%d: %s", i+1, total, vmName)

// Deletion operations
logger.Delete("Removing VM %s", vmName)

// Skip operations
logger.Skip("Skipping VM cleanup (TEST_CLUSTER_CLEANUP not enabled)")

// Detailed debugging
logger.Debug("SSH command output: %s", output)
```

### Structured Logging with Fields

```go
// Add context to a logger
vmLogger := logger.WithFields(map[string]interface{}{
    "vm":        vmName,
    "namespace": namespace,
    "phase":     vm.Status.Phase,
})

vmLogger.Info("VM status updated")
// Output: [INFO] VM status updated [vm=test-vm, namespace=default, phase=Running]
```

### Direct `slog` API

You can also use the standard `slog` API:

```go
log := logger.GetLogger()
log.Info("message", "key", "value", "num", 42)
log.Error("error occurred", "error", err)
```

## Output Examples

### Console Output (with emojis and colors)

```
[INFO]  ▶️ Step 1: Loading cluster configuration
[INFO]  ✅ Step 1 Complete: Cluster configuration loaded successfully
[INFO]  ⏳ Waiting for VM 1/3: master-1
[INFO]  ✅ VM master-1 is Running
[WARN]  ⚠️ Resource already exists, skipping creation
[ERROR] ❌ Failed to create VM: connection timeout
[DEBUG] 🐛 SSH command output: total 42K...
```

### Console Output (without emojis, USE_EMOJIS=false)

```
[INFO]  Step 1: Loading cluster configuration
[INFO]  Step 1 Complete: Cluster configuration loaded successfully
[INFO]  Waiting for VM 1/3: master-1
[INFO]  VM master-1 is Running
[WARN]  Resource already exists, skipping creation
[ERROR] Failed to create VM: connection timeout
[DEBUG] SSH command output: total 42K...
```

### File Output (JSON)

```json
{"time":"2025-01-12T15:04:05Z","level":"INFO","msg":"▶️ Step 1: Loading cluster configuration","type":"step"}
{"time":"2025-01-12T15:04:06Z","level":"INFO","msg":"✅ Step 1 Complete: Cluster configuration loaded","type":"step_complete"}
{"time":"2025-01-12T15:04:07Z","level":"ERROR","msg":"❌ Failed to create VM","type":"error","error":"connection timeout"}
```

## Testing

Create a test logger that writes to a buffer:

```go
func TestMyFunction(t *testing.T) {
    var buf bytes.Buffer
    testLogger := logger.NewTestLogger(&buf, slog.LevelDebug)
    logger.SetLogger(testLogger)

    // Run your code...
    MyFunction()

    // Check output
    output := buf.String()
    if !strings.Contains(output, "expected message") {
        t.Errorf("Expected message not found in log output")
    }
}
```

## Migration from fmt.Printf

The package is designed to make migration easy:

**Before:**
```go
fmt.Printf("    ▶️  Step 1: Loading configuration from %s\n", filename)
fmt.Printf("    ✅ Step 1: Configuration loaded\n")
fmt.Printf("    ❌ Failed to load: %v\n", err)
```

**After:**
```go
logger.Step(1, "Loading configuration from %s", filename)
logger.StepComplete(1, "Configuration loaded")
logger.Error("Failed to load: %v", err)
```

## Log Levels Usage Guidelines

### DEBUG Level
- Detailed debug information
- Success messages
- General info messages
- Progress indicators (waiting, polling)
- Skip operations
- Delete operations
- Detailed SSH command outputs
- File upload/download details
- Resource inspection details
- Bootstrap log content

### INFO Level (Default)
- Major step start/completion (Step, StepComplete)
- Important workflow milestones

### WARN Level
- Resources already exist (skipping)
- Fallback behaviors
- Non-critical cleanup issues
- Deprecated usage warnings

### ERROR Level
- Operation failures
- Resource creation/deletion failures
- Connection/SSH failures
- Test failures

## Implementation Details

The logger uses:
- **Console Handler**: Custom `slog.Handler` with emoji and color support
- **File Handler**: Standard `slog.JSONHandler` for structured output
- **Multi Handler**: Combines multiple handlers for dual output
- **Level Filtering**: Respects log level at handler level for performance
