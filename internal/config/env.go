// Environment variables used by codebase

package config

import (
	"os"
)

var (
	// ssh passphrase for ssh private key used to connect to base cluster
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")

	// KubeConfigPath is the path to a kubeconfig file. If SSH retrieval fails (e.g., sudo requires password),
	// this path will be used as a fallback. If not set and SSH fails, the user will be notified to download
	// the kubeconfig manually and set this environment variable.
	KubeConfigPath = os.Getenv("KUBE_CONFIG_PATH")
)
