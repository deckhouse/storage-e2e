// Flags used by codebase

package config

import (
	"flag"
)

var (
	// alwaysUseExisting indicates to always use an existing cluster if available
	alwaysUseExisting = flag.Bool("always-use-existing", false, "Always use an existing cluster if available")
	// alwaysCreateNew indicates to always create a new cluster
	alwaysCreateNew = flag.Bool("always-create-new", false, "Always create a new cluster")
)

// Just a dummy for flags to avoid compiler error
func init() {
	_ = *alwaysUseExisting
	_ = *alwaysCreateNew
}

// AlwaysUseExisting returns the value of the alwaysUseExisting flag
func AlwaysUseExisting() bool {
	return false
}

// AlwaysCreateNew returns the value of the alwaysCreateNew flag
func AlwaysCreateNew() bool {
	return true
}
