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

	// ENVIRONMENT VARIABLES DEFINITIONS

	// YAMLConfigFilename is the filename of the YAML configuration file
	YAMLConfigFilename             = os.Getenv("YAML_CONFIG_FILENAME")
	YAMLConfigFilenameDefaultValue = "cluster_config.yml"

	// SSH credentials to connect to BASE cluster
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")

	SSHUser             = os.Getenv("SSH_USER")
	SSHUserDefaultValue = "a.yakubov"

	SSHKeyPath             = os.Getenv("SSH_KEY_PATH")
	SSHKeyPathDefaultValue = "~/.ssh/id_rsa"

	SSHHost             = os.Getenv("SSH_HOST")
	SSHHostDefaultValue = "94.26.231.181"

	// SSH credentials to deploy to VM
	VMSSHUser             = os.Getenv("SSH_VM_USER")
	VMSSHUserDefaultValue = "cloud"

	VMSSHPublicKey             = os.Getenv("SSH_VM_PUBLIC_KEY")
	VMSSHPublicKeyDefaultValue = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC8WyGvnBNQp+v6CUweF1QYCRtR7Do/IA8IA2uMd2HuBsddFrc5xYon2ZtEvypZC4Vm1CzgcgUm9UkHgxytKEB4zOOWkmqFP62OSLNyuWMaFEW1fb0EDenup6B5SrjnA8ckm4Hf2NSLvwW9yS98TfN3nqPOPJKfQsN+OTiCerTtNyXjca//ppuGKsQd99jG7SqE9aDQ3sYCXatM53SXqhxS2nTew82bmzVmKXDxcIzVrS9f+2WmXIdY2cKo2I352yKWOIp1Nk0uji8ozLPHFQGvbAG8DGG1KNVcBl2qYUcttmCpN+iXEcGqyn/atUVJJMnZXGtp0fiL1rMLqAd/bb6TFNzZFSsS+zqGesxqLePe32vLCQ3xursP3BRZkrScM+JzIqevfP63INHJEZfYlUf4Ic+gfliS2yA1LwhU7hD4LSVXMQynlF9WeGjuv6ZYxmO8hC6IWCqWnIUqKUiGtvBSPXwsZo7wgljBr4ykJgBzS9MjZ0fzz1JKe80tH6clpjIOn6ReBPwQBq2zmDDrpa5GVqqqjXhRQuA0AfpHdhs5UKxs1PBr7/PTLA7PI39xkOAE/Zj1TYQ2dmqvpskshi7AtBStjinQBAlLXysLSHBtO+3+PLAYcMZMVfb0bVqfGGludO2prvXrrWWTku0eOsA5IRahrRdGhv5zhKgFV7cwUQ== ayakubov@MacBook-Pro-Alexey.local"

	// KubeConfigPath is the path to a kubeconfig file. If SSH retrieval fails (e.g., sudo requires password),
	// this path will be used as a fallback. If not set and SSH fails, the user will be notified to download
	// the kubeconfig manually and set this environment variable, test will fail.
	KubeConfigPath = os.Getenv("KUBE_CONFIG_PATH")

	// TestClusterCreateMode specifies the cluster creation mode. Must be set to either "alwaysUseExisting" or "alwaysCreateNew". If not set, test will fail.
	TestClusterCreateMode = os.Getenv("TEST_CLUSTER_CREATE_MODE")

	// TestClusterCleanup specifies whether to remove the test cluster after tests complete.
	// Default is "false". If set to "true" or "True", the test cluster will be cleaned up after tests.
	TestClusterCleanup             = os.Getenv("TEST_CLUSTER_CLEANUP")
	TestClusterCleanupDefaultValue = "false"

	// TestClusterNamespace specifies the namespace for DKP cluster deployment
	TestClusterNamespace             = os.Getenv("TEST_CLUSTER_NAMESPACE")
	TestClusterNamespaceDefaultValue = "e2e-test-cluster"

	// TestClusterStorageClass specifies the storage class for DKP cluster deployment
	TestClusterStorageClass             = os.Getenv("TEST_CLUSTER_STORAGE_CLASS")
	TestClusterStorageClassDefaultValue = "rsc-test-r2-local"

	// DKPLicenseKey specifies the DKP license key for cluster deployment
	DKPLicenseKey = os.Getenv("DKP_LICENSE_KEY")

	// CONFIGURATION VARIABLES DEFINITIONS

	// DefaultSetupVM is the default VM configuration of the node that is used for bootstrap of test cluster.
	// This VM is always created separately and should be deleted after cluster bootstrap.
	DefaultSetupVM = ClusterNode{
		Hostname: "bootstrap-node-",
		HostType: HostTypeVM,
		Role:     ClusterRoleSetup,
		OSType:   OSTypeMap["Ubuntu 22.04 6.2.0-39-generic"],
		CPU:      2,
		RAM:      4,
		DiskSize: 20,
	}
)

func ValidateEnvironment() error {
	// Default values for environment variables
	if YAMLConfigFilename == "" {
		YAMLConfigFilename = YAMLConfigFilenameDefaultValue
	}

	if TestClusterCleanup == "" || TestClusterCleanup != "true" && TestClusterCleanup != "True" {
		TestClusterCleanup = TestClusterCleanupDefaultValue
	}

	if SSHKeyPath == "" {
		SSHKeyPath = SSHKeyPathDefaultValue
	}
	if SSHUser == "" {
		SSHUser = SSHUserDefaultValue
	}
	if SSHHost == "" {
		SSHHost = SSHHostDefaultValue
	}
	if VMSSHUser == "" {
		VMSSHUser = VMSSHUserDefaultValue
	}
	if VMSSHPublicKey == "" {
		VMSSHPublicKey = VMSSHPublicKeyDefaultValue
	}
	if TestClusterNamespace == "" {
		TestClusterNamespace = TestClusterNamespaceDefaultValue
	}
	if TestClusterStorageClass == "" {
		TestClusterStorageClass = TestClusterStorageClassDefaultValue
	}

	// There are no default values for these variables and they must be set! Otherwise, the test will fail.
	if DKPLicenseKey == "" {
		return fmt.Errorf("DKP_LICENSE_KEY environment variable is required but not set. ")
	}

	if TestClusterCreateMode == "" {
		return fmt.Errorf("TEST_CLUSTER_CREATE_MODE environment variable is required but not set. "+
			"Please set it to either '%s' or '%s'",
			ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew)
	}

	if TestClusterCreateMode != ClusterCreateModeAlwaysUseExisting && TestClusterCreateMode != ClusterCreateModeAlwaysCreateNew {
		return fmt.Errorf("TEST_CLUSTER_CREATE_MODE has invalid value '%s'. "+
			"Must be either '%s' or '%s'",
			TestClusterCreateMode, ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew)
	}

	return nil
}
