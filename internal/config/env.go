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
	// YAMLConfigFilename is the filename of the YAML configuration file
	YAMLConfigFilename = os.Getenv("YAML_CONFIG_FILENAME")

	// SSH credentials to connect to BASE cluster
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")
	SSHUser       = os.Getenv("SSH_USER")
	SSHKeyPath    = os.Getenv("SSH_KEY_PATH")
	SSHHost       = os.Getenv("SSH_HOST")

	// SSH credentials to deploy to VM
	VMSSHUser      = os.Getenv("SSH_VM_USER")
	VMSSHPublicKey = os.Getenv("SSH_VM_PUBLIC_KEY")

	// KubeConfigPath is the path to a kubeconfig file. If SSH retrieval fails (e.g., sudo requires password),
	// this path will be used as a fallback. If not set and SSH fails, the user will be notified to download
	// the kubeconfig manually and set this environment variable.
	KubeConfigPath = os.Getenv("KUBE_CONFIG_PATH")

	// ClusterCreateMode specifies the cluster creation mode. Must be set to either "alwaysUseExisting" or "alwaysCreateNew"
	ClusterCreateMode = os.Getenv("CLUSTER_CREATE_MODE")

	// TestClusterCleanup specifies whether to remove the test cluster after tests complete.
	// Default is "false". If set to "true" or "True", the test cluster will be cleaned up after tests.
	TestClusterCleanup = os.Getenv("TEST_CLUSTER_CLEANUP")

	// DKPLicenseKey specifies the DKP license key for cluster deployment
	DKPLicenseKey = os.Getenv("DKP_LICENSE_KEY")
)

func ValidateEnvironment() error {
	// Default values for environment variables
	if YAMLConfigFilename == "" {
		YAMLConfigFilename = "cluster_config.yml"
	}

	if TestClusterCleanup != "true" && TestClusterCleanup != "True" {
		TestClusterCleanup = "false"
	}

	if SSHKeyPath == "" {
		SSHKeyPath = "~/.ssh/id_rsa"
	}
	if SSHUser == "" {
		SSHUser = "a.yakubov"
	}
	if SSHHost == "" {
		SSHHost = "94.26.231.181"
	}
	if VMSSHUser == "" {
		VMSSHUser = "cloud"
	}
	if VMSSHPublicKey == "" {
		VMSSHPublicKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC8WyGvnBNQp+v6CUweF1QYCRtR7Do/IA8IA2uMd2HuBsddFrc5xYon2ZtEvypZC4Vm1CzgcgUm9UkHgxytKEB4zOOWkmqFP62OSLNyuWMaFEW1fb0EDenup6B5SrjnA8ckm4Hf2NSLvwW9yS98TfN3nqPOPJKfQsN+OTiCerTtNyXjca//ppuGKsQd99jG7SqE9aDQ3sYCXatM53SXqhxS2nTew82bmzVmKXDxcIzVrS9f+2WmXIdY2cKo2I352yKWOIp1Nk0uji8ozLPHFQGvbAG8DGG1KNVcBl2qYUcttmCpN+iXEcGqyn/atUVJJMnZXGtp0fiL1rMLqAd/bb6TFNzZFSsS+zqGesxqLePe32vLCQ3xursP3BRZkrScM+JzIqevfP63INHJEZfYlUf4Ic+gfliS2yA1LwhU7hD4LSVXMQynlF9WeGjuv6ZYxmO8hC6IWCqWnIUqKUiGtvBSPXwsZo7wgljBr4ykJgBzS9MjZ0fzz1JKe80tH6clpjIOn6ReBPwQBq2zmDDrpa5GVqqqjXhRQuA0AfpHdhs5UKxs1PBr7/PTLA7PI39xkOAE/Zj1TYQ2dmqvpskshi7AtBStjinQBAlLXysLSHBtO+3+PLAYcMZMVfb0bVqfGGludO2prvXrrWWTku0eOsA5IRahrRdGhv5zhKgFV7cwUQ== ayakubov@MacBook-Pro-Alexey.local"
	}

	// There are no default values for these variables and they must be set! Otherwise, the test will fail.
	if DKPLicenseKey == "" {
		return fmt.Errorf("DKP_LICENSE_KEY environment variable is required but not set. ")
	}

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
