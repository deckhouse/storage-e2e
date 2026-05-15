/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// LoadClusterConfig loads and validates a cluster configuration from a YAML file
// The config file is expected to be in the same directory as the caller (typically the test file)
func LoadClusterConfig(configFilename string) (*config.ClusterDefinition, error) {
	// Get the caller's file path (the test file that called this function)
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return nil, fmt.Errorf("failed to determine caller file path")
	}
	callerDir := filepath.Dir(callerFile)
	yamlConfigPath := filepath.Join(callerDir, configFilename)

	// Read the YAML file
	data, err := os.ReadFile(yamlConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", yamlConfigPath, err)
	}

	// Parse YAML directly into ClusterDefinition (has custom UnmarshalYAML for root key)
	var clusterDef config.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Expand ${VAR} placeholders in modulePullOverride fields. CI uses this to
	// pass a per-PR/MR image tag via a single env var (e.g. MODULE_IMAGE_TAG)
	// without editing the YAML between runs. Missing envs fail fast here so we
	// don't silently regress to "main" on accidentally unset variables.
	if err := config.ExpandEnvInModulePullOverride(&clusterDef); err != nil {
		return nil, fmt.Errorf("expand env in modulePullOverride: %w", err)
	}

	// Validate the configuration
	if err := validateClusterConfig(&clusterDef); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &clusterDef, nil
}

// validateClusterConfig validates the cluster configuration
func validateClusterConfig(cfg *config.ClusterDefinition) error {
	// Validate that at least one master exists
	if len(cfg.Masters) == 0 {
		return fmt.Errorf("at least one master node is required")
	}

	// Validate master nodes
	for i, master := range cfg.Masters {
		if err := validateNode(master, true); err != nil {
			return fmt.Errorf("master[%d] validation failed: %w", i, err)
		}
	}

	// Validate worker nodes
	for i, worker := range cfg.Workers {
		if err := validateNode(worker, false); err != nil {
			return fmt.Errorf("worker[%d] validation failed: %w", i, err)
		}
	}

	// Validate setup node if present
	if cfg.Setup != nil {
		if err := validateNode(*cfg.Setup, false); err != nil {
			return fmt.Errorf("setup node validation failed: %w", err)
		}
	}

	// Validate DKP parameters
	dkpParams := cfg.DKPParameters
	if dkpParams.PodSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.podSubnetCIDR is required")
	}
	if dkpParams.ServiceSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.serviceSubnetCIDR is required")
	}
	if dkpParams.ClusterDomain == "" {
		return fmt.Errorf("dkpParameters.clusterDomain is required")
	}
	if dkpParams.RegistryRepo == "" {
		return fmt.Errorf("dkpParameters.registryRepo is required")
	}

	return nil
}

// validateNode validates a single node configuration
func validateNode(node config.ClusterNode, isMaster bool) error {
	if node.Hostname == "" {
		return fmt.Errorf("hostname is required")
	}

	if node.HostType == config.HostTypeVM {
		if node.CPU <= 0 {
			return fmt.Errorf("CPU must be greater than 0 for VM nodes")
		}
		if node.RAM <= 0 {
			return fmt.Errorf("RAM must be greater than 0 for VM nodes")
		}
		if node.DiskSize <= 0 {
			return fmt.Errorf("diskSize must be greater than 0 for VM nodes")
		}
	}

	return nil
}

// expandPath expands ~ to home directory and resolves symlinks if present
func expandPath(path string) (string, error) {
	var expandedPath string

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}

		if path == "~" {
			expandedPath = homeDir
		} else {
			expandedPath = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
		}
	} else {
		expandedPath = path
	}

	// Resolve symlinks if present (usually it won't be a symlink)
	// If resolution fails (e.g., path doesn't exist or is not a symlink), use the expanded path
	resolvedPath, err := filepath.EvalSymlinks(expandedPath)
	if err != nil {
		// Path might not exist yet or might not be a symlink - use expanded path as-is
		return expandedPath, nil
	}

	return resolvedPath, nil
}

// getKubeconfigRemoteShell prints kubeconfig for use with client-go. It prefers
// /etc/kubernetes/super-admin.conf (Kubernetes 1.29+ unified kubeconfig) when the file
// exists, and falls back to /etc/kubernetes/admin.conf otherwise.
//
// The two `sudo -n /bin/cat ...` invocations are intentionally NOT wrapped in
// `sudo -n sh -c '...'`. With a wrapper the privileged binary is /bin/sh, so a
// minimal sudoers rule of the form
//
//	user ALL=(root) NOPASSWD: /bin/cat /etc/kubernetes/super-admin.conf, /bin/cat /etc/kubernetes/admin.conf
//
// would NOT match and sudo would still ask for a password. By calling /bin/cat
// directly we make this command work with the same fine-grained NOPASSWD rule
// that the buildKubeconfigFetchError diagnostic recommends. The 2>/dev/null on
// the first try suppresses the "permission denied / no such file" noise so the
// fallback to admin.conf produces clean kubeconfig content on stdout; stderr
// from the final failed attempt is preserved by ExecCapture for diagnostics.
const getKubeconfigRemoteShell = "sudo -n /bin/cat /etc/kubernetes/super-admin.conf 2>/dev/null " +
	"|| sudo -n /bin/cat /etc/kubernetes/admin.conf"

// GetKubeconfig connects to the master node via SSH, retrieves kubeconfig (preferring
// super-admin.conf over admin.conf when available), and returns a rest.Config that can
// be used with Kubernetes clients, along with the path to the kubeconfig file.
// If sshClient is provided, it will be used instead of creating a new connection.
// If sshClient is nil, a new connection will be created and closed automatically.
// If kubeconfigOutputDir is non-empty, the kubeconfig file is written there; otherwise /tmp/e2e/ is used.
func GetKubeconfig(ctx context.Context, masterIP, user, keyPath string, sshClient ssh.SSHClient, kubeconfigOutputDir string) (*rest.Config, string, error) {
	// Create SSH client if not provided
	shouldClose := false
	if sshClient == nil {
		var err error
		sshClient, err = ssh.NewClient(user, masterIP, keyPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create SSH client: %w", err)
		}
		shouldClose = true
	}
	if shouldClose {
		defer sshClient.Close()
	}

	outputDir := kubeconfigOutputDir
	if outputDir == "" {
		outputDir = config.E2ETempDir
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create directory %s: %w", outputDir, err)
	}

	kubeconfigPath := filepath.Join(outputDir, fmt.Sprintf("kubeconfig-%s.yml", masterIP))

	var (
		kubeconfigContent []byte
		// kubeconfigSource is a short, human-readable tag identifying where the
		// kubeconfig came from. It's printed at the end of GetKubeconfig so it
		// is always obvious in test logs which cluster we're actually about to
		// hit — important after diagnosing wrong-cluster bugs that look like
		// "stale lock" or "unexpected modules".
		kubeconfigSource string
	)

	// Read kubeconfig via SSH: prefer super-admin.conf when present (see getKubeconfigRemoteShell).
	kubeconfigContentStr, kubeconfigStderr, sshErr := sshClient.ExecCapture(ctx, getKubeconfigRemoteShell)
	switch {
	case sshErr == nil:
		// SSH succeeded - use the content from SSH
		kubeconfigContent = []byte(kubeconfigContentStr)
		kubeconfigSource = fmt.Sprintf("SSH(%s@%s:/etc/kubernetes/{super-admin,admin}.conf)", user, masterIP)

	case config.KubeConfigPath != "":
		// SSH retrieval failed (likely due to sudo password requirement) and the
		// caller pointed us at a specific kubeconfig file via KUBE_CONFIG_PATH.
		resolvedPath, expandErr := expandPath(config.KubeConfigPath)
		if expandErr != nil {
			return nil, "", fmt.Errorf("failed to expand KUBE_CONFIG_PATH (%s): %w", config.KubeConfigPath, expandErr)
		}
		readContent, readErr := os.ReadFile(resolvedPath)
		if readErr != nil {
			return nil, "", fmt.Errorf("failed to read kubeconfig from KUBE_CONFIG_PATH (%s): %w", resolvedPath, readErr)
		}
		kubeconfigContent = readContent
		kubeconfigSource = fmt.Sprintf("KUBE_CONFIG_PATH=%s", resolvedPath)

	default:
		// SSH failed and the caller did not opt into a specific kubeconfig via
		// KUBE_CONFIG_PATH. Fail fast rather than silently picking up the
		// developer's ~/.kube/config / $KUBECONFIG, which has historically
		// caused tests to acquire stale locks on unrelated SAN clusters or
		// deploy modules against the wrong stand. Classify the failed attempt's
		// stderr so the returned error tells the operator which knob to turn.
		cause := classifyKubeconfigFetchFailure(ctx, sshErr, kubeconfigStderr)
		return nil, "", buildKubeconfigFetchError(user, masterIP, sshErr, cause)
	}

	// Always stamp the kubeconfig source + the resulting current-context/server
	// in the log. With this single line a developer reading the output knows
	// for sure which cluster the test is about to talk to, regardless of which
	// of the three resolution paths fired above.
	finalCtx, finalServer := kubeconfigContextSummary(kubeconfigContent)
	logger.Info("Loaded kubeconfig (source=%s, current-context=%q, server=%q)", kubeconfigSource, finalCtx, finalServer)

	// Write kubeconfig content to file (always write a working copy, regardless of source)
	kubeconfigFile, err := os.Create(kubeconfigPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create kubeconfig file %s: %w", kubeconfigPath, err)
	}

	if _, err := kubeconfigFile.Write(kubeconfigContent); err != nil {
		kubeconfigFile.Close()
		return nil, "", fmt.Errorf("failed to write kubeconfig to file: %w", err)
	}
	if err := kubeconfigFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close kubeconfig file: %w", err)
	}

	// Build rest.Config from the kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	// Configure extended timeouts for tunnel-based connections
	configureExtendedTimeouts(config)

	return config, kubeconfigPath, nil
}

// configureExtendedTimeouts configures rest.Config with extended timeouts for tunnel-based connections
// This helps prevent timeouts when API server is under load or network latency is high
func configureExtendedTimeouts(config *rest.Config) {
	// Increase overall request timeout from default 30s to 2 minutes
	config.Timeout = 2 * time.Minute

	// Wrap the transport to extend timeouts without breaking authentication
	// We preserve the existing WrapTransport if any, and wrap on top of it
	originalWrapTransport := config.WrapTransport
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		// First apply original wrapper if it exists
		if originalWrapTransport != nil {
			rt = originalWrapTransport(rt)
		}

		// Then modify transport timeouts if it's an http.Transport
		if httpTransport, ok := rt.(*http.Transport); ok {
			// Clone transport to avoid modifying shared instances
			clonedTransport := httpTransport.Clone()
			clonedTransport.TLSHandshakeTimeout = 30 * time.Second   // Extended from default 10s
			clonedTransport.ResponseHeaderTimeout = 60 * time.Second // Wait up to 60s for response headers
			clonedTransport.IdleConnTimeout = 90 * time.Second
			return clonedTransport
		}

		return rt
	}
}

// UpdateKubeconfigPort updates the kubeconfig file to use the specified local port
// It replaces the server URL with 127.0.0.1:port
func UpdateKubeconfigPort(kubeconfigPath string, localPort int) error {
	content, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig file: %w", err)
	}

	contentStr := string(content)
	// Replace server URL with localhost and new port
	// Common patterns: server: https://<ip>:6445 or server: https://127.0.0.1:6445
	// Also handle:    server: https://<ip>:6443 (standard k8s port)
	lines := strings.Split(contentStr, "\n")
	updated := false
	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "server:") {
			// Replace the entire server URL with 127.0.0.1:port
			// Pattern: server: https://<host>:<port>
			if strings.Contains(trimmedLine, "https://") {
				// Find the URL part and replace it
				urlStart := strings.Index(trimmedLine, "https://")
				if urlStart != -1 {
					// Replace the URL with localhost:port
					// Preserve any indentation before "server:"
					indent := ""
					for j := 0; j < len(line) && (line[j] == ' ' || line[j] == '\t'); j++ {
						indent += string(line[j])
					}
					newURL := fmt.Sprintf("https://127.0.0.1:%d", localPort)
					lines[i] = indent + "server: " + newURL
					updated = true
				}
			}
		}
	}

	if !updated {
		return fmt.Errorf("could not find server URL in kubeconfig to update")
	}

	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(kubeconfigPath, []byte(newContent), 0600); err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	return nil
}

// kubeconfigContextSummary parses a serialized kubeconfig and returns its
// current-context name and the matching cluster's `server:` URL. Used purely
// for human-readable log lines that identify which cluster the test is about
// to talk to. On any parse failure the helper returns "<unknown>" / "<unknown>"
// rather than an error: failing here would defeat its only purpose, which is
// to make the surrounding log message safer to print under partial failures.
func kubeconfigContextSummary(content []byte) (currentContext, server string) {
	currentContext = "<unknown>"
	server = "<unknown>"
	if len(content) == 0 {
		return
	}
	cfg, err := clientcmd.Load(content)
	if err != nil || cfg == nil {
		return
	}
	if cfg.CurrentContext != "" {
		currentContext = cfg.CurrentContext
	}
	if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
		if cl, ok := cfg.Clusters[ctx.Cluster]; ok && cl != nil && cl.Server != "" {
			server = cl.Server
		}
	}
	return
}

// kubeconfigFetchCause discriminates the most likely reason
// getKubeconfigRemoteShell exited non-zero. Used solely to choose the
// human-readable error template — the original SSH error is always
// preserved via %w wrapping, so callers' errors.Is/errors.As keep working.
type kubeconfigFetchCause int

const (
	causeUnknown kubeconfigFetchCause = iota
	causeKubeconfigMissing
	causeSudoPasswordRequired
	causePermissionDenied
	causeSSHUnreachable
	causeContextCanceled
)

func (c kubeconfigFetchCause) String() string {
	switch c {
	case causeKubeconfigMissing:
		return "kubeconfig file is missing on the host"
	case causeSudoPasswordRequired:
		return "sudo requires a password (sudo -n failed)"
	case causePermissionDenied:
		return "permission denied reading kubeconfig"
	case causeSSHUnreachable:
		return "SSH connection failed"
	case causeContextCanceled:
		return "context canceled"
	default:
		return "unknown"
	}
}

// classifyKubeconfigFetchFailure inspects the stderr of the failed read attempt
// and maps it to a cause. It does not perform extra round-trips: the caller
// already knows the read failed and passes the captured stderr here.
func classifyKubeconfigFetchFailure(ctx context.Context, sshErr error, stderr string) kubeconfigFetchCause {
	if ctx.Err() != nil {
		return causeContextCanceled
	}

	var exitErr *gossh.ExitError
	if sshErr != nil && !errors.As(sshErr, &exitErr) {
		return causeSSHUnreachable
	}

	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "a password is required"),
		strings.Contains(s, "a terminal is required"),
		strings.Contains(s, "sudo: no tty present"):
		return causeSudoPasswordRequired
	case strings.Contains(s, "no such file or directory"):
		return causeKubeconfigMissing
	case strings.Contains(s, "permission denied"):
		return causePermissionDenied
	default:
		return causeUnknown
	}
}

// buildKubeconfigFetchError renders an actionable, multi-line error for
// the caller to print. Each branch lists the same kind of remediation
// (sudoers tweak, KUBE_CONFIG_PATH escape, SSH check) but in the order
// most relevant for the detected cause. The returned error always wraps
// the original sshErr so errors.Is(err, &ssh.ExitError{...}) still works.
func buildKubeconfigFetchError(user, masterIP string, sshErr error, cause kubeconfigFetchCause) error {
	sudoersLine := fmt.Sprintf(
		"%s ALL=(root) NOPASSWD: /bin/cat /etc/kubernetes/super-admin.conf, /bin/cat /etc/kubernetes/admin.conf",
		user,
	)
	sudoersFix := "echo '" + sudoersLine + "' | sudo tee /etc/sudoers.d/e2e-kubeconfig && sudo chmod 0440 /etc/sudoers.d/e2e-kubeconfig"

	switch cause {
	case causeContextCanceled:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s): "+
				"the test context was canceled before kubeconfig could be fetched "+
				"(suite timeout, Ctrl+C, or parent context done).\n"+
				"Pick ONE remedy:\n"+
				"  1) Increase the test/suite timeout if bootstrap or module enablement is still running.\n"+
				"  2) Set KUBE_CONFIG_PATH to skip SSH fetch on subsequent runs:\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"Original SSH error: %w",
			user, masterIP, sshErr)

	case causeSSHUnreachable:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s): "+
				"the master is not reachable over SSH (connection refused, timeout, or handshake failure).\n"+
				"Pick ONE remedy:\n"+
				"  1) Verify SSH_HOST / SSH_JUMP_HOST, VPN, and that the VM is up.\n"+
				"  2) Confirm key-based auth works: ssh -i \"$SSH_PRIVATE_KEY\" %s@%s true\n"+
				"  3) Set KUBE_CONFIG_PATH to a local kubeconfig instead:\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"Original SSH error: %w",
			user, masterIP, user, masterIP, sshErr)

	case causeSudoPasswordRequired:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s): "+
				"sudo on the master requires a password (sudo -n exited non-zero).\n"+
				"Pick ONE remedy:\n"+
				"  1) Allow passwordless cat of the two kubeconfig files (run on the master):\n"+
				"       %s\n"+
				"  2) Point the test at a local kubeconfig instead (no SSH/sudo at all):\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"Original SSH error: %w",
			user, masterIP, sudoersFix, sshErr)

	case causeKubeconfigMissing:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s): "+
				"neither /etc/kubernetes/super-admin.conf nor /etc/kubernetes/admin.conf exists on the host — "+
				"this looks like a non-control-plane node.\n"+
				"Pick ONE remedy:\n"+
				"  1) Make sure SSH_HOST points at a Kubernetes control-plane (master) node "+
				"(check SSH_HOST/SSH_USER, and SSH_JUMP_HOST if you use one).\n"+
				"  2) Set KUBE_CONFIG_PATH to a kubeconfig file on your local machine:\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"Original SSH error: %w",
			user, masterIP, sshErr)

	case causePermissionDenied:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s): "+
				"permission denied reading /etc/kubernetes/{super-admin,admin}.conf even though the files exist.\n"+
				"Pick ONE remedy:\n"+
				"  1) Ensure the NOPASSWD rule targets /bin/cat directly (not /bin/sh), then re-run:\n"+
				"       %s\n"+
				"  2) Set KUBE_CONFIG_PATH to a kubeconfig file on your local machine:\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"Original SSH error: %w",
			user, masterIP, sudoersFix, sshErr)

	default:
		return fmt.Errorf(
			"failed to read kubeconfig from master via SSH (%s@%s) "+
				"and KUBE_CONFIG_PATH is not set.\n"+
				"Pick ONE remedy:\n"+
				"  1) If sudo on the master requires a password, allow passwordless cat of the kubeconfig files:\n"+
				"       %s\n"+
				"  2) Set KUBE_CONFIG_PATH to a kubeconfig file on your local machine:\n"+
				"       export KUBE_CONFIG_PATH=$HOME/.kube/config\n"+
				"  3) Fix SSH credentials so the master is reachable as %s with key-based auth.\n"+
				"Original SSH error: %w",
			user, masterIP, sudoersFix, user, sshErr)
	}
}
