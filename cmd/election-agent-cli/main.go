package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/automaxprocs/maxprocs"
)

var (
	Hostname string
	ctx      = context.Background()
)

var rootCmd = &cobra.Command{
	Use:          "election-agent-cli",
	Long:         "Election agent command line tool",
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&Hostname, "host", "h", "", "election agent hostname")
	rootCmd.PersistentFlags().Bool("help", false, "help for "+rootCmd.Name())

	_, err := maxprocs.Set()
	if err != nil {
		fmt.Printf("Failed to set GOMAXPROCS, error: %s\n", err)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		reportError(err)
		os.Exit(1)
	}
}

func reportError(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err.Error())
	os.Exit(1)
}
