// Environment variables used by codebase

package config

import (
	"fmt"
	"os"
)

const (
	// ClusterCreateModeAlwaysUseExisting indicates to always use an existing cluster if available
	ClusterCreateModeAlwaysUseExisting = "alwaysUseExisting"
	// ClusterCreateModeAlwaysCreateNew indicates to always create a new cluster
	ClusterCreateModeAlwaysCreateNew = "alwaysCreateNew"
)

var (
	// ssh passphrase for ssh private key used to connect to base cluster
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")

	// KubeConfigPath is the path to a kubeconfig file. If SSH retrieval fails (e.g., sudo requires password),
	// this path will be used as a fallback. If not set and SSH fails, the user will be notified to download
	// the kubeconfig manually and set this environment variable.
	KubeConfigPath = os.Getenv("KUBE_CONFIG_PATH")

	// ClusterCreateMode specifies the cluster creation mode. Must be set to either "alwaysUseExisting" or "alwaysCreateNew"
	ClusterCreateMode = os.Getenv("CLUSTER_CREATE_MODE")

	// AutoGenerateVMNames specifies whether to auto-generate VM names or use provided in config.
	//  Default is "false". If set to "true", the VM names suffix in kubernetes style will be added to VM names set in cluster config.
	AutoGenerateVMNames = os.Getenv("AUTO_GENERATE_VM_NAMES") // TODO implement this in cluster.LoadClusterConfig function.
)

// ValidateClusterCreateMode validates that CLUSTER_CREATE_MODE is set and has a valid value
func ValidateClusterCreateMode() error {
	if ClusterCreateMode == "" {
		return fmt.Errorf("CLUSTER_CREATE_MODE environment variable is required but not set. "+
			"Please set it to either '%s' or '%s'",
			ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew)
	}

	if ClusterCreateMode != ClusterCreateModeAlwaysUseExisting && ClusterCreateMode != ClusterCreateModeAlwaysCreateNew {
		return fmt.Errorf("CLUSTER_CREATE_MODE has invalid value '%s'. "+
			"Must be either '%s' or '%s'",
			ClusterCreateMode, ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew)
	}

	return nil
}
