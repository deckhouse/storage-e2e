// Environment variables used by codebase

package config

import (
	"fmt"
	"os"
)

const (
	// ClusterCreateModeAlwaysUseExisting indicates to always use an existing cluster if available
	ClusterCreateModeAlwaysUseExisting = "alwaysUseExisting"

	// TODO
	// useExisting - Vasya's option
	// useExistingWithChecks - my option - with check of vm, os, kernel etc.

	// ClusterCreateModeAlwaysCreateNew indicates to always create a new cluster
	ClusterCreateModeAlwaysCreateNew = "alwaysCreateNew"
	// ClusterCreateModeCommander indicates to create or use a cluster from Deckhouse Commander
	ClusterCreateModeCommander = "commander"

	// ImagePullPolicyAlways indicates to always create ClusterVirtualImage and fail if it exists
	ImagePullPolicyAlways = "Always"
	// ImagePullPolicyIfNotExists indicates to use existing ClusterVirtualImage without warnings if it exists
	ImagePullPolicyIfNotExists = "IfNotExists"

	// LogLevelDebug indicates debug log level
	LogLevelDebug = "debug"
	// LogLevelInfo indicates info log level
	LogLevelInfo = "info"
	// LogLevelWarn indicates warn log level
	LogLevelWarn = "warn"
	// LogLevelError indicates error log level
	LogLevelError = "error"

	// E2ETempDir is the directory for temporary e2e artifacts (kubeconfig, cluster-state, bootstrap config/log)
	E2ETempDir = "/tmp/e2e"
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

	// Private key. Path to the private key file.
	SSHPrivateKey             = os.Getenv("SSH_PRIVATE_KEY")
	SSHPrivateKeyDefaultValue = "~/.ssh/id_rsa"

	// Public key. Can be either path to a file or a plain-text string.
	SSHPublicKey             = os.Getenv("SSH_PUBLIC_KEY")
	SSHPublicKeyDefaultValue = "~/.ssh/id_rsa.pub"

	// Base cluster SSH host
	SSHHost = os.Getenv("SSH_HOST")

	// Jump host configuration for connecting to existing clusters behind a bastion/jump host
	// When set, SSH will first connect to the jump host, then to the target cluster
	SSHJumpHost    = os.Getenv("SSH_JUMP_HOST")     // Jump host address (optional)
	SSHJumpUser    = os.Getenv("SSH_JUMP_USER")     // Jump host user (optional, defaults to SSH_USER if jump host is set)
	SSHJumpKeyPath = os.Getenv("SSH_JUMP_KEY_PATH") // Jump host SSH key path (optional, defaults to SSH_PRIVATE_KEY if jump host is set)

	// SSH credentials to deploy to VM
	VMSSHUser             = os.Getenv("SSH_VM_USER")
	VMSSHUserDefaultValue = "cloud"
	// VMSSHPassword when set is used to SSH from jump host to VMs (cloud@vmIP) via sshpass. Leave empty for key-based auth.
	VMSSHPassword = os.Getenv("SSH_VM_PASSWORD")

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

	// TestClusterResume when set to "true" or "True" (only for alwaysCreateNew) tries to continue from a previous
	// failed run: if state was saved after step 6 (VMs created, IPs gathered), connects to the first master and
	// runs remaining steps (add nodes, enable modules). Set to "true" and re-run the test after a mid-deploy failure.
	TestClusterResume = os.Getenv("TEST_CLUSTER_RESUME")

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

	// LogLevel specifies the log level. Can be debug, info, warn, error. Default is info.
	LogLevel             = os.Getenv("LOG_LEVEL")
	LogLevelDefaultValue = LogLevelDebug

	// LogFilePath specifies the path to the log file. If not set or empty, logs only to console.
	// If set, logs to both console and the specified file.
	LogFilePath = os.Getenv("LOG_FILE_PATH")

	// Deckhouse Commander configuration
	// CommanderURL specifies the URL of the Deckhouse Commander API (e.g., "https://commander.example.com")
	CommanderURL = os.Getenv("COMMANDER_URL")

	// CommanderToken specifies the API token for authentication with Deckhouse Commander
	CommanderToken = os.Getenv("COMMANDER_TOKEN")

	// CommanderClusterName specifies the name of the cluster in Commander to use or create
	CommanderClusterName             = os.Getenv("COMMANDER_CLUSTER_NAME")
	CommanderClusterNameDefaultValue = "e2e-test-cluster"

	// CommanderTemplateName specifies the name of the template to use for creating a new cluster in Commander
	CommanderTemplateName = os.Getenv("COMMANDER_TEMPLATE_NAME")

	// CommanderTemplateVersion specifies the version of the template to use (optional, defaults to latest)
	CommanderTemplateVersion = os.Getenv("COMMANDER_TEMPLATE_VERSION")

	// CommanderRegistryName specifies the name of the registry to use for cluster creation.
	// The registry_id will be automatically resolved by looking up this name in Commander.
	// If COMMANDER_VALUES already contains registry_id, this setting is ignored.
	CommanderRegistryName = os.Getenv("COMMANDER_REGISTRY_NAME")

	// CommanderCreateIfNotExists specifies whether to create a new cluster if it doesn't exist in Commander
	// Default is "false". If set to "true", a new cluster will be created using the specified template.
	CommanderCreateIfNotExists             = os.Getenv("COMMANDER_CREATE_IF_NOT_EXISTS")
	CommanderCreateIfNotExistsDefaultValue = "false"

	// CommanderWaitTimeout specifies the timeout for waiting for cluster to become ready in Commander (default: 30m)
	CommanderWaitTimeout             = os.Getenv("COMMANDER_WAIT_TIMEOUT")
	CommanderWaitTimeoutDefaultValue = "30m"

	// CommanderInsecureSkipTLSVerify specifies whether to skip TLS certificate verification for Commander API.
	// Set to "true" when using self-signed certificates. Default is "false".
	CommanderInsecureSkipTLSVerify             = os.Getenv("COMMANDER_INSECURE_SKIP_TLS_VERIFY")
	CommanderInsecureSkipTLSVerifyDefaultValue = "false"

	// CommanderCACert specifies the path to a CA certificate file for verifying Commander API TLS certificate.
	// If set, this certificate will be used to verify the server certificate instead of system CAs.
	// This is an alternative to COMMANDER_INSECURE_SKIP_TLS_VERIFY for self-signed certificates.
	CommanderCACert = os.Getenv("COMMANDER_CA_CERT")

	// CommanderAuthMethod specifies the authentication method for Commander API.
	// See: https://deckhouse.io/modules/commander/stable/integration_api.html
	// Supported values:
	// - "x-auth-token" (default): X-Auth-Token: <token> (recommended by Commander docs)
	// - "bearer": Authorization: Bearer <token>
	// - "token": Authorization: Token <token>
	// - "cookie": Cookie: token=<token>
	// - "basic": Authorization: Basic <base64(user:token)> (requires COMMANDER_AUTH_USER)
	CommanderAuthMethod             = os.Getenv("COMMANDER_AUTH_METHOD")
	CommanderAuthMethodDefaultValue = "x-auth-token"

	// CommanderAuthUser specifies the username for basic authentication (only used with auth_method=basic)
	CommanderAuthUser = os.Getenv("COMMANDER_AUTH_USER")

	// CommanderAPIPrefix specifies the API path prefix for Commander API.
	// Default is "/api/v1". Try "/api" or "" if the default doesn't work.
	CommanderAPIPrefix             = os.Getenv("COMMANDER_API_PREFIX")
	CommanderAPIPrefixDefaultValue = "/api/v1"

	// CommanderInputValues specifies template input values for cluster creation as JSON string.
	// See: https://deckhouse.io/modules/commander/stable/integration_api.html
	// Example: '{"releaseChannel": "EarlyAccess", "kubeVersion": "1.29", "slot": {...}}'
	// These values are template-specific parameters defined in cluster_template_versions.params.
	// If not specified, the system will use empty values (template defaults will be used).
	CommanderInputValues = os.Getenv("COMMANDER_VALUES")

	// Stress test configuration environment variables
	// STRESS_TEST_PVC_SIZE specifies the initial PVC size for stress tests (default: "100Mi")
	StressTestPVCSize             = os.Getenv("STRESS_TEST_PVC_SIZE")
	StressTestPVCSizeDefaultValue = "100Mi"

	// STRESS_TEST_PODS_COUNT specifies the number of pods to create in stress tests (default: 100)
	StressTestPodsCount             = os.Getenv("STRESS_TEST_PODS_COUNT")
	StressTestPodsCountDefaultValue = "100"

	// STRESS_TEST_PVC_SIZE_AFTER_RESIZE specifies the PVC size after first resize (default: "200Mi")
	StressTestPVCSizeAfterResize             = os.Getenv("STRESS_TEST_PVC_SIZE_AFTER_RESIZE")
	StressTestPVCSizeAfterResizeDefaultValue = "200Mi"

	// STRESS_TEST_PVC_SIZE_AFTER_RESIZE_STAGE2 specifies the PVC size after second resize (default: "300Mi")
	StressTestPVCSizeAfterResizeStage2             = os.Getenv("STRESS_TEST_PVC_SIZE_AFTER_RESIZE_STAGE2")
	StressTestPVCSizeAfterResizeStage2DefaultValue = "300Mi"

	// STRESS_TEST_SNAPSHOTS_PER_PVC specifies the number of snapshots per PVC (default: 2)
	StressTestSnapshotsPerPVC             = os.Getenv("STRESS_TEST_SNAPSHOTS_PER_PVC")
	StressTestSnapshotsPerPVCDefaultValue = "2"

	// STRESS_TEST_MAX_ATTEMPTS specifies the maximum number of attempts for waiting operations (default: 360)
	StressTestMaxAttempts             = os.Getenv("STRESS_TEST_MAX_ATTEMPTS")
	StressTestMaxAttemptsDefaultValue = "360"

	// STRESS_TEST_INTERVAL specifies the interval between attempts in seconds (default: 5)
	StressTestInterval             = os.Getenv("STRESS_TEST_INTERVAL")
	StressTestIntervalDefaultValue = "5"

	// STRESS_TEST_CLEANUP specifies whether to cleanup resources after stress tests (default: "true")
	StressTestCleanup             = os.Getenv("STRESS_TEST_CLEANUP")
	StressTestCleanupDefaultValue = "true"

	// LogTimetampsEnabled specifies whether to include timestamps in log output (default: "true")
	LogTimetampsEnabled             = os.Getenv("LOG_TIMESTAMPS_ENABLED")
	LogTimetampsEnabledDefaultValue = "true"
)

// ApplyDefaults populates package-level config variables that have a documented
// default value but were not provided through the environment. It is idempotent
// and safe to call multiple times.
//
// Suites that don't call ValidateEnvironment() (because they don't need its
// required-variable checks) should still call ApplyDefaults() — otherwise
// optional variables like SSH_VM_USER stay empty and propagate as user="" all
// the way to the SSH server, where it shows up as "Invalid user" / publickey
// rejection that is hard to attribute to a missing default.
func ApplyDefaults() {
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
}

func ValidateEnvironment() error {
	ApplyDefaults()

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
			"Please set it to '%s', '%s', or '%s'",
			ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew, ClusterCreateModeCommander)
	}

	if TestClusterCreateMode != ClusterCreateModeAlwaysUseExisting &&
		TestClusterCreateMode != ClusterCreateModeAlwaysCreateNew &&
		TestClusterCreateMode != ClusterCreateModeCommander {
		return fmt.Errorf("TEST_CLUSTER_CREATE_MODE has invalid value '%s'. "+
			"Must be '%s', '%s', or '%s'",
			TestClusterCreateMode, ClusterCreateModeAlwaysUseExisting, ClusterCreateModeAlwaysCreateNew, ClusterCreateModeCommander)
	}

	// Validate Commander-specific environment variables when in Commander mode
	if TestClusterCreateMode == ClusterCreateModeCommander {
		if CommanderURL == "" {
			return fmt.Errorf("COMMANDER_URL environment variable is required when TEST_CLUSTER_CREATE_MODE is '%s'", ClusterCreateModeCommander)
		}
		if CommanderToken == "" {
			return fmt.Errorf("COMMANDER_TOKEN environment variable is required when TEST_CLUSTER_CREATE_MODE is '%s'", ClusterCreateModeCommander)
		}
		if CommanderClusterName == "" {
			CommanderClusterName = CommanderClusterNameDefaultValue
		}
		if CommanderCreateIfNotExists == "" {
			CommanderCreateIfNotExists = CommanderCreateIfNotExistsDefaultValue
		}
		if CommanderWaitTimeout == "" {
			CommanderWaitTimeout = CommanderWaitTimeoutDefaultValue
		}
		// If creating a new cluster, template name is required
		if CommanderCreateIfNotExists == "true" && CommanderTemplateName == "" {
			return fmt.Errorf("COMMANDER_TEMPLATE_NAME environment variable is required when COMMANDER_CREATE_IF_NOT_EXISTS is 'true'")
		}
	}

	if LogLevel == "" {
		LogLevel = LogLevelDefaultValue
	}

	if LogLevel != LogLevelDebug && LogLevel != LogLevelInfo && LogLevel != LogLevelWarn && LogLevel != LogLevelError {
		return fmt.Errorf("LOG_LEVEL has invalid value '%s'. "+
			"Must be either '%s' or '%s' or '%s' or '%s'",
			LogLevel, LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError)
	}

	if LogTimetampsEnabled == "" {
		LogTimetampsEnabled = LogTimetampsEnabledDefaultValue
	}

	if LogTimetampsEnabled != "true" && LogTimetampsEnabled != "false" {
		return fmt.Errorf("LOG_TIMESTAMPS_ENABLED has invalid value '%s'. "+
			"Must be either '%s' or '%s'",
			LogTimetampsEnabled, "true", "false")
	}

	return nil
}
