package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/spf13/cobra"

	"election-agent/internal/agent"
	eagrpc "election-agent/proto/election_agent/v1"
)

func init() {
	rootCmd.AddCommand(controlCmd)

	controlCmd.AddCommand(getStatusCmd)
	controlCmd.AddCommand(getActiveZoneCmd)
	controlCmd.AddCommand(setStatusCmd)
}

var controlCmd = &cobra.Command{
	Use:   "control [command]",
	Short: "Election agent control operations",
}

var getStatusCmd = &cobra.Command{
	Use:   "get-status",
	Short: "Get election agent status information",
	RunE:  getStatus,
}

func getStatus(cmd *cobra.Command, args []string) error {
	if host == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(ctx, host)
	if err != nil {
		reportError(err)
	}

	ret, err := client.Control.GetStatus(ctx, &eagrpc.Empty{})
	if err != nil {
		reportError(err)
		return err
	}
	output, err := marshalJSON(ret)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
	return nil
}

var getActiveZoneCmd = &cobra.Command{
	Use:   "get-active-zone [url]",
	Short: "Get zone coordinator active zone",
	Args:  cobra.ExactArgs(1),
	RunE:  getActiveZone,
}

func getActiveZone(cmd *cobra.Command, args []string) error {
	url := args[0]
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("%s\n", body)
	return nil
}

var setStatusCmd = &cobra.Command{
	Use:   "set-status <state> <mode> [zoom_enable]",
	Short: "Set election agent status",
	Long:  "Set election agent status\nArgument format:\n  <state>: active|standby\n  <mode>: normal|orphan\n  [zoom_enable]: true|false|1|0, defaults to true",
	Args:  cobra.MinimumNArgs(2),
	RunE:  setStatus,
}

func setStatus(cmd *cobra.Command, args []string) error {
	if host == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	if !slices.Contains(agent.ValidStates, args[0]) {
		return fmt.Errorf("invalid state %s", args[0])
	}
	if !slices.Contains(agent.ValidModes, args[1]) {
		return fmt.Errorf("invalid mode %s", args[1])
	}
	zoomEnable := true
	if len(args) >= 3 {
		if args[2] == "false" || args[2] == "0" {
			zoomEnable = false
		}
	}

	client, err := newGrpcClient(ctx, host)
	if err != nil {
		reportError(err)
	}
	req := &eagrpc.AgentStatus{
		State:      args[0],
		Mode:       args[1],
		ZoomEnable: zoomEnable,
	}
	result, err := client.Control.SetStatus(ctx, req)
	if err != nil {
		reportError(err)
	}

	output, err := marshalJSON(result)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
	return nil
}
