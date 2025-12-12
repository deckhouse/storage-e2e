// Environment variables used by codebase

package config

import (
	"os"
)

var (
	SSHPassphrase = os.Getenv("SSH_PASSPHRASE")
)
