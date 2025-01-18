package main

import (
	"os"

	"github.com/autobrr/mkbrr/cmd"
)

// Version information set by build flags
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	cmd.SetVersion(version, buildTime)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
