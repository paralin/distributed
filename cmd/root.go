package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/fuserobotics/distributed/pkg/daemon"
)

var homeDir string

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "distributed",
	Short: "Docker distribution management API and state machine.",
	Long:  `Manages a local Distribution server. Loads a configuration file. Config can be updated by the API.`,
	Run: func(cmd *cobra.Command, args []string) {
		s := daemon.System{HomeDir: homeDir}
		os.Exit(s.Main())
	},
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags, which, if defined here,
	// will be global for your application.

	RootCmd.PersistentFlags().StringVar(&homeDir, "home", "", "home dir (default is /etc/distributed)")
}

func initConfig() {
	if homeDir != "" {
		homeDir = filepath.Clean(homeDir)
		homeDirAbs, err := filepath.Abs(homeDir)
		if err != nil {
			fmt.Errorf("Unable to format %s to absolute path, %s, using default path.\n", homeDir, err)
			homeDir = ""
		} else {
			homeDir = homeDirAbs
		}
	}

	if homeDir == "" {
		homeDir = "/etc/distributed"
	}
}
