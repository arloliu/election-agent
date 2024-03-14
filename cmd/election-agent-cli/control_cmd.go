package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"election-agent/internal/agent"
	eagrpc "election-agent/proto/election_agent/v1"
)

func init() {
	rootCmd.AddCommand(controlCmd)

	controlCmd.AddCommand(getActiveZoneCmd)
	controlCmd.AddCommand(getStatusCmd)
	controlCmd.AddCommand(setStatusCmd)
}

var controlCmd = &cobra.Command{
	Use:   "control [command]",
	Short: "Election agent control operations",
}

var getActiveZoneCmd = &cobra.Command{
	Use:   "get-active-zone [url]",
	Short: "Get zone coordinator active zone",
	Args:  cobra.ExactArgs(1),
	RunE:  getActiveZone,
}

func getActiveZone(cmd *cobra.Command, args []string) error {
	url := args[0]

	ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("%s\n", body)
	return nil
}

var getStatusCmd = &cobra.Command{
	Use:   "get-status",
	Short: "Get election agent status information",
	RunE:  getStatus,
}

func getGRPCTargets(hostname string) ([]string, error) {
	host, port, err := net.SplitHostPort(hostname)
	if err != nil {
		return nil, err
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}

	targets := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		targets = append(targets, net.JoinHostPort(addr.String(), port))
	}

	return targets, nil
}

func getStatus(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	targets, err := getGRPCTargets(Hostname)
	if err != nil {
		return err
	}

	status := make([]*eagrpc.AgentStatus, 0, len(targets))
	for _, target := range targets {
		client, err := newGrpcClient(ctx, target)
		if err != nil {
			reportError(err)
		}

		ret, err := client.Control.GetStatus(ctx, &eagrpc.Empty{})
		if err != nil {
			reportError(err)
			return err
		}
		status = append(status, ret)
	}
	output, err := marshalJSON(status)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
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
	if Hostname == "" {
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

	client, err := newGrpcClient(ctx, Hostname)
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

	output, err := marshalProtoJSON(result)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
	return nil
}
