// Flags used by codebase

package config

import (
	"flag"
	"fmt"
	"os"
)

var (
	// alwaysUseExisting indicates to always use an existing cluster if available
	alwaysUseExisting = flag.Bool("alwaysUseExisting", false, "Always use an existing cluster if available")
	// alwaysCreateNew indicates to always create a new cluster
	alwaysCreateNew = flag.Bool("alwaysCreateNew", false, "Always create a new cluster")
)

// init registers flags, aliases, and validates that at least one of alwaysUseExisting or alwaysCreateNew is set
func init() {
	// Register short aliases for flags
	flag.BoolVar(alwaysUseExisting, "e", false, "Alias for -alwaysUseExisting")
	flag.BoolVar(alwaysCreateNew, "n", false, "Alias for -alwaysCreateNew")

	flag.Usage = usage
	flag.Parse()

	// Validate that at least one of the flags is set
	if (!*alwaysUseExisting && !*alwaysCreateNew) || (*alwaysUseExisting && *alwaysCreateNew) {
		fmt.Fprintf(os.Stderr, "Error: Either --alwaysUseExisting (-e) or --alwaysCreateNew (-n) must be set, but not both\n\n")
		flag.Usage()
		os.Exit(1)
	}
}

// usage prints the usage information for the command-line flags
func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Options:\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, "\nNote: Either -alwaysUseExisting or -alwaysCreateNew must be set, but not both together!\n")
}

// AlwaysUseExisting returns the value of the alwaysUseExisting flag
func AlwaysUseExisting() bool {
	return *alwaysUseExisting
}

// AlwaysCreateNew returns the value of the alwaysCreateNew flag
func AlwaysCreateNew() bool {
	return *alwaysCreateNew
}
