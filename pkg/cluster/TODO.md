# TODOs for the package

## Error with module enablement

### Error

```bash
• [FAILED] [24.831 seconds]
Cluster Creation Step-by-Step Test [It] should enable and configure modules from cluster definition in test cluster
/Users/ayakubov/development/e2e/storage-e2e/tests/cluster-creation-by-steps/cluster_creation_test.go:553

  [FAILED] Failed to enable and configure modules
  Unexpected error:
      <*fmt.wrapError | 0x14000112700>:
      failed to create moduleconfig sds-replicated-volume: failed to create moduleconfig sds-replicated-volume: Internal error occurred: failed calling webhook "module-configs.deckhouse-webhook.deckhouse.io": failed to call webhook: Post "https://deckhouse.d8-system.svc:4223/validate/v1alpha1/module-configs?timeout=10s": dial tcp 10.225.43.103:4223: connect: connection refused
      {
          msg: "failed to create moduleconfig sds-replicated-volume: failed to create moduleconfig sds-replicated-volume: Internal error occurred: failed calling webhook \"module-configs.deckhouse-webhook.deckhouse.io\": failed to call webhook: Post \"https://deckhouse.d8-system.svc:4223/validate/v1alpha1/module-configs?timeout=10s\": dial tcp 10.225.43.103:4223: connect: connection refused",
          err: <*fmt.wrapError | 0x140001126e0>{
              msg: "failed to create moduleconfig sds-replicated-volume: Internal error occurred: failed calling webhook \"module-configs.deckhouse-webhook.deckhouse.io\": failed to call webhook: Post \"https://deckhouse.d8-system.svc:4223/validate/v1alpha1/module-configs?timeout=10s\": dial tcp 10.225.43.103:4223: connect: connection refused",
              err: <*errors.StatusError | 0x14000440aa0>{
                  ErrStatus: {
                      TypeMeta: {Kind: "", APIVersion: ""},
                      ListMeta: {
                          SelfLink: "",
                          ResourceVersion: "",
                          Continue: "",
                          RemainingItemCount: nil,
                      },
                      Status: "Failure",
                      Message: "Internal error occurred: failed calling webhook \"module-configs.deckhouse-webhook.deckhouse.io\": failed to call webhook: Post \"https://deckhouse.d8-system.svc:4223/validate/v1alpha1/module-configs?timeout=10s\": dial tcp 10.225.43.103:4223: connect: connection refused",
                      Reason: "InternalError",
                      Details: {
                          Name: "",
                          Group: "",
                          Kind: "",
                          UID: "",
                          Causes: [
                              {
                                  Type: "",
                                  Message: "failed calling webhook \"module-configs.deckhouse-webhook.deckhouse.io\": failed to call webhook: Post \"https://deckhouse.d8-system.svc:4223/validate/v1alpha1/module-configs?timeout=10s\": dial tcp 10.225.43.103:4223: connect: connection refused",
                                  Field: "",
                              },
                          ],
                          RetryAfterSeconds: 0,
                      },
                      Code: 500,
                  },
              },
          },
      }
  occurred
  In [It] at: /Users/ayakubov/development/e2e/storage-e2e/tests/cluster-creation-by-steps/cluster_creation_test.go:563 @ 12/23/25 12:12:45.876
------------------------------
S [SKIPPED] [0.000 seconds]
Cluster Creation Step-by-Step Test [It] should wait for all modules to be ready in test cluster
/Users/ayakubov/development/e2e/storage-e2e/tests/cluster-creation-by-steps/cluster_creation_test.go:569

  [SKIPPED] Spec skipped because an earlier spec in an ordered container failed
  In [It] at: /Users/ayakubov/development/e2e/storage-e2e/tests/cluster-creation-by-steps/cluster_creation_test.go:569 @ 12/23/25 12:13:06.569
------------------------------

Summarizing 1 Failure:
  [FAIL] Cluster Creation Step-by-Step Test [It] should enable and configure modules from cluster definition in test cluster
  /Users/ayakubov/development/e2e/storage-e2e/tests/cluster-creation-by-steps/cluster_creation_test.go:563
```

### Code

```go
// It retries on webhook connection errors to handle cases where the webhook service isn't ready yet
func configureModuleConfig(ctx context.Context, kubeconfig *rest.Config, moduleConfig *config.ModuleConfig) error {
	settings := make(map[string]interface{})
	if moduleConfig.Settings != nil {
		settings = moduleConfig.Settings
	}
      // Check if ModuleConfig exists
    _, err := deckhouse.GetModuleConfig(ctx, kubeconfig, moduleConfig.Name)
    if err != nil {
        // Resource doesn't exist, create it
        if err := deckhouse.CreateModuleConfig(ctx, kubeconfig, moduleConfig.Name, moduleConfig.Version, moduleConfig.Enabled, settings); err != nil {
            return fmt.Errorf("failed to create moduleconfig %s: %w", moduleConfig.Name, err)
        }
    } else {
        // Resource exists, update it
        if err := deckhouse.UpdateModuleConfig(ctx, kubeconfig, moduleConfig.Name, moduleConfig.Version, moduleConfig.Enabled, settings); err != nil {
            return fmt.Errorf("failed to update moduleconfig %s: %w", moduleConfig.Name, err)
        }

```

Need to fix the issue without ssh! (Fix temporarily with kubectl apply -f via ssh. It's not a good approach!)

