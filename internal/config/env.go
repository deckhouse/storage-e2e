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

	// ImagePullPolicyAlways indicates to always create ClusterVirtualImage and fail if it exists
	ImagePullPolicyAlways = "Always"
	// ImagePullPolicyIfNotExists indicates to use existing ClusterVirtualImage without warnings if it exists
	ImagePullPolicyIfNotExists = "IfNotExists"
)

var (

	// ENVIRONMENT VARIABLES DEFINITIONS

	// YAMLConfigFilename is the filename of the YAML configuration file
	YAMLConfigFilename             = os.Getenv("YAML_CONFIG_FILENAME")
	YAMLConfigFilenameDefaultValue = "cluster_config.yml"

	// SSH credentials to connect to BASE cluster
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")

	SSHUser = os.Getenv("SSH_USER")
	//SSHUserDefaultValue = "a.yakubov"

	// Private key. Can be either path for a file or a base64 encoded string.
	SSHPrivateKey             = os.Getenv("SSH_PRIVATE_KEY")
	SSHPrivateKeyDefaultValue = "~/.ssh/id_rsa"

	// Public key. Can be either path to a file or a plain-text string.
	SSHPublicKey             = os.Getenv("SSH_PUBLIC_KEY")
	SSHPublicKeyDefaultValue = "~/.ssh/id_rsa.pub"

	// Base cluster SSH host
	SSHHost = os.Getenv("SSH_HOST")

	// SSH credentials to deploy to VM
	VMSSHUser             = os.Getenv("SSH_VM_USER")
	VMSSHUserDefaultValue = "cloud"

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
	TestClusterStorageClass = os.Getenv("TEST_CLUSTER_STORAGE_CLASS")
	//TestClusterStorageClassDefaultValue = "rsc-test-r2-local"

	// DKPLicenseKey specifies the DKP license key for cluster deployment
	DKPLicenseKey = os.Getenv("DKP_LICENSE_KEY")

	// RegistryDockerCfg specifies the docker registry key to download images from Deckhouse registry.
	RegistryDockerCfg = os.Getenv("REGISTRY_DOCKER_CFG")

	// Defines if the code will pull images for CVI or use existing ones. Can be always and ifNotExists. Default is ifNotExists.
	ImagePullPolicy             = os.Getenv("IMAGE_PULL_POLICY")
	ImagePullPolicyDefaultValue = ImagePullPolicyIfNotExists
)

func ValidateEnvironment() error {
	// Default values for environment variables
	if YAMLConfigFilename == "" {
		YAMLConfigFilename = YAMLConfigFilenameDefaultValue
	}

	if TestClusterCleanup == "" || TestClusterCleanup != "true" && TestClusterCleanup != "True" {
		TestClusterCleanup = TestClusterCleanupDefaultValue
	}

	if SSHPrivateKey == "" {
		SSHPrivateKey = SSHPrivateKeyDefaultValue
	}
	if VMSSHUser == "" {
		VMSSHUser = VMSSHUserDefaultValue
	}
	if SSHPublicKey == "" {
		SSHPublicKey = SSHPublicKeyDefaultValue
	}
	if TestClusterNamespace == "" {
		TestClusterNamespace = TestClusterNamespaceDefaultValue
	}

	// There are no default values for these variables and they must be set! Otherwise, the test will fail.
	if SSHUser == "" {
		return fmt.Errorf("SSH_USER environment variable is required but not set.")
	}

	if SSHHost == "" {
		return fmt.Errorf("SSH_HOST environment variable is required but not set.")
	}

	if TestClusterStorageClass == "" {
		return fmt.Errorf("TEST_CLUSTER_STORAGE_CLASS environment variable is required but not set.")
	}

	if DKPLicenseKey == "" {
		return fmt.Errorf("DKP_LICENSE_KEY environment variable is required but not set. ")
	}

	if RegistryDockerCfg == "" {
		return fmt.Errorf("REGISTRY_DOCKER_CFG environment variable is required but not set.")
	}

	if ImagePullPolicy == "" {
		ImagePullPolicy = ImagePullPolicyDefaultValue
	}

	if ImagePullPolicy != ImagePullPolicyAlways && ImagePullPolicy != ImagePullPolicyIfNotExists {
		return fmt.Errorf("IMAGE_PULL_POLICY has invalid value '%s'. "+
			"Must be either '%s' or '%s'",
			ImagePullPolicy, ImagePullPolicyAlways, ImagePullPolicyIfNotExists)
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
