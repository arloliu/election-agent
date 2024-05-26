package main

import (
	"election-agent/cmd/election-agent-cli/latency"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.AddCommand(latency.LatencyCmd)
}

var testCmd = &cobra.Command{
	Use:   "test [command]",
	Short: "Test election agent",
}
