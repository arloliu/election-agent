package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	eagrpc "election-agent/proto/election_agent/v1"
)

func init() {
	rootCmd.AddCommand(electCmd)

	electCmd.AddCommand(campaignCmd)
	electCmd.AddCommand(extendCmd)
	electCmd.AddCommand(resignCmd)
	electCmd.AddCommand(handoverCmd)
	electCmd.AddCommand(leaderCmd)
	electCmd.AddCommand(leadersCmd)
	electCmd.AddCommand(podsCmd)
}

var electCmd = &cobra.Command{
	Use:   "elect [command]",
	Short: "Election agent election operations",
}

var campaignCmd = &cobra.Command{
	Use:   "campaign <election> <candidate> <term>",
	Short: "Campaign election",
	Args:  cobra.ExactArgs(3),
	RunE:  campaign,
}

func campaign(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	term, err := parseInt[int32](args[2])
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.CampaignRequest{
		Election:  args[0],
		Candidate: args[1],
		Term:      term,
	}
	result, err := client.Election.Campaign(ctx, req)
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

var extendCmd = &cobra.Command{
	Use:   "extend <election> <leader> <term> [retries] [retry interval]",
	Short: "Extend the elected term",
	Args:  cobra.MinimumNArgs(3),
	RunE:  extendElectedTerm,
}

func extendElectedTerm(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	term, err := parseInt[int32](args[2])
	if err != nil {
		reportError(err)
	}

	var retries int32 = 0
	var retry_interval int32 = 0
	if len(args) >= 4 {
		var err error
		retries, err = parseInt[int32](args[3])
		if err != nil {
			reportError(err)
		}
	}

	if len(args) >= 5 {
		var err error
		retry_interval, err = parseInt[int32](args[4])
		if err != nil {
			reportError(err)
		}
	}

	req := &eagrpc.ExtendElectedTermRequest{
		Election:      args[0],
		Leader:        args[1],
		Term:          term,
		Retries:       retries,
		RetryInterval: retry_interval,
	}
	result, err := client.Election.ExtendElectedTerm(ctx, req)
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

var resignCmd = &cobra.Command{
	Use:   "resign <election> <leader>",
	Short: "Resign the election",
	Args:  cobra.ExactArgs(2),
	RunE:  resign,
}

func resign(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.ResignRequest{
		Election: args[0],
		Leader:   args[1],
	}
	result, err := client.Election.Resign(ctx, req)
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

var handoverCmd = &cobra.Command{
	Use:   "handover <election> <leader> <term>",
	Short: "Handover election to new leader",
	Args:  cobra.ExactArgs(3),
	RunE:  handover,
}

func handover(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	term, err := parseInt[int32](args[2])
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.HandoverRequest{
		Election: args[0],
		Leader:   args[1],
		Term:     term,
	}
	result, err := client.Election.Handover(ctx, req)
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

var leaderCmd = &cobra.Command{
	Use:   "leader <election>",
	Short: "Get the election leader",
	Args:  cobra.ExactArgs(1),
	RunE:  getLeader,
}

func getLeader(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.GetLeaderRequest{
		Election: args[0],
	}
	result, err := client.Election.GetLeader(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			fmt.Println(`{"value":""}`)
			return nil
		}
		reportError(err)
	}

	output, err := marshalProtoJSON(result)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
	return nil
}

var leadersCmd = &cobra.Command{
	Use:   "leaders <kind>",
	Short: "Get the election leaders by kind",
	Args:  cobra.ExactArgs(1),
	RunE:  getLeaders,
}

func getLeaders(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.ListLeadersRequest{
		Kind: args[0],
	}
	result, err := client.Election.ListLeaders(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			fmt.Println(`{"leaders":[]}`)
			return nil
		}
		reportError(err)
	}

	output, err := marshalProtoJSON(result)
	if err != nil {
		reportError(err)
	}

	fmt.Println(output)
	return nil
}

var podsCmd = &cobra.Command{
	Use:   "pods <namespace> <deployment>",
	Short: "Get a list of pod information",
	Args:  cobra.ExactArgs(2),
	RunE:  getPods,
}

func getPods(cmd *cobra.Command, args []string) error {
	if Hostname == "" {
		return errors.New("hostname is required, please use -h or --host to specify hostname")
	}

	client, err := newGrpcClient(Hostname)
	if err != nil {
		reportError(err)
	}

	req := &eagrpc.GetPodsRequest{
		Namespace:  args[0],
		Deployment: args[1],
	}
	result, err := client.Election.GetPods(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			fmt.Println(`[]`)
			return nil
		}
		reportError(err)
	}

	output, err := marshalProtoJSON(result)
	if err != nil {
		reportError(err)
	}
	fmt.Println(output)
	return nil
}
