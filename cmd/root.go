package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const banner = `         __   ___.                 
  _____ |  | _\_ |________________ 
 /     \|  |/ /| __ \_  __ \_  __ \
|  Y Y  \    < | \_\ \  | \/|  | \/
|__|_|  /__|_ \|___  /__|   |__|   
      \/     \/    \/              `

var (
	version   string
	buildTime string
)

var rootCmd = &cobra.Command{
	Use:   "mkbrr",
	Short: "A tool to inspect and create torrent files",
	Long:  banner + "\n\nmkbrr is a tool to create and inspect torrent files.",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Version: %s\n", version)
		fmt.Printf("Build Time: %s\n", buildTime)
	},
}

// SetVersion sets the version information
func SetVersion(v, bt string) {
	version = v
	buildTime = bt
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() error {
	// disable completion command
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// add version command
	rootCmd.AddCommand(versionCmd)

	// set custom usage template to control command order
	rootCmd.SetUsageTemplate(`Usage:
  {{.CommandPath}} [command]

Available Commands:
  create      Create a new torrent file
  inspect     Inspect a torrent file
  version     Print version information{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}

Use "{{.CommandPath}} [command] --help" for more information about a command.
`)

	return rootCmd.Execute()
}
