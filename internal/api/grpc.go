package api

import (
	"context"
	"fmt"
	"slices"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/zone"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "election-agent/proto/election_agent/v1"
)

type ElectionGRPCService struct {
	pb.UnimplementedElectionServer
	cfg        *config.Config
	leaseMgr   *lease.LeaseManager
	kubeClient kube.KubeClient
}

func newElectionGRPCService(cfg *config.Config, leaseMgr *lease.LeaseManager, kubeClient kube.KubeClient) *ElectionGRPCService {
	return &ElectionGRPCService{cfg: cfg, leaseMgr: leaseMgr, kubeClient: kubeClient}
}

func (s *ElectionGRPCService) Campaign(ctx context.Context, req *pb.CampaignRequest) (*pb.CampaignResult, error) {
	if req.Election == "" {
		return &pb.CampaignResult{}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Candidate == "" {
		return &pb.CampaignResult{}, status.Errorf(codes.InvalidArgument, "Empty field 'candidate'")
	}
	if req.Term < 1000 {
		return &pb.CampaignResult{}, status.Errorf(codes.InvalidArgument, "The field 'term' must >= 1000")
	}

	err := s.leaseMgr.GrantLease(ctx, req.Election, req.Candidate, time.Duration(int64(req.Term)*int64(time.Millisecond)))
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.CampaignResult{}, status.Error(codes.Unavailable, err.Error())
		}
		return &pb.CampaignResult{}, nil
	}

	return &pb.CampaignResult{Elected: true, Leader: req.Candidate}, nil
}

func (s *ElectionGRPCService) ExtendElectedTerm(ctx context.Context, req *pb.ExtendElectedTermRequest) (*pb.BoolValue, error) {
	if req.Election == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'leader'")
	}
	if req.Term < 1000 {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "The field 'term' must >= 1000")
	}

	err := s.leaseMgr.ExtendLease(ctx, req.Election, req.Leader, time.Duration(req.Term)*time.Millisecond)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.Unavailable, err.Error())
		}
		return &pb.BoolValue{Value: false}, status.Errorf(codes.NotFound, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) Resign(ctx context.Context, req *pb.ResignRequest) (*pb.BoolValue, error) {
	if req.Election == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'leader'")
	}

	err := s.leaseMgr.RevokeLease(ctx, req.Election, req.Leader)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.Unavailable, err.Error())
		}
		return &pb.BoolValue{Value: false}, status.Errorf(codes.NotFound, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) GetLeader(ctx context.Context, req *pb.GetLeaderRequest) (*pb.StringValue, error) {
	if req.Election == "" {
		return &pb.StringValue{Value: ""}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}

	leader, err := s.leaseMgr.GetLeaseHolder(ctx, req.Election)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.StringValue{Value: ""}, status.Error(codes.Unavailable, err.Error())
		}
		return &pb.StringValue{Value: ""}, status.Errorf(codes.NotFound, err.Error())
	}

	return &pb.StringValue{Value: leader}, nil
}

func (s *ElectionGRPCService) GetPods(ctx context.Context, req *pb.GetPodsRequest) (*pb.Pods, error) {
	if !s.cfg.Kube.Enable || s.kubeClient == nil {
		return nil, status.Errorf(codes.Unimplemented, "method GetPods not implemented")
	}
	return s.kubeClient.GetPods(req.Namespace, req.Deployment)
}

type ControlGRPCService struct {
	pb.UnimplementedControlServer
	cfg      *config.Config
	leaseMgr *lease.LeaseManager
	zoneMgr  zone.ZoneManager
}

func newControlGRPCService(cfg *config.Config, leaseMgr *lease.LeaseManager, zoneMgr zone.ZoneManager) *ControlGRPCService {
	return &ControlGRPCService{cfg: cfg, leaseMgr: leaseMgr, zoneMgr: zoneMgr}
}

func (s *ControlGRPCService) GetStatus(ctx context.Context, req *pb.Empty) (*pb.AgentStatus, error) {
	state, err := s.zoneMgr.GetAgentState()
	if err != nil {
		return &pb.AgentStatus{}, status.Errorf(codes.Unavailable, err.Error())
	}

	mode, err := s.zoneMgr.GetAgentMode()
	if err != nil {
		return &pb.AgentStatus{State: state}, status.Errorf(codes.Unavailable, err.Error())
	}

	enable, err := s.zoneMgr.GetZoomEnable()
	if err != nil {
		return &pb.AgentStatus{State: state, Mode: mode}, status.Errorf(codes.Unavailable, err.Error())
	}

	return &pb.AgentStatus{State: state, Mode: mode, ZoomEnable: enable}, nil
}

func (s *ControlGRPCService) SetStatus(ctx context.Context, state *pb.AgentStatus) (*pb.BoolValue, error) {
	if !slices.Contains(agent.ValidStates, state.State) {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Invalid state:%s", state.State))
	}
	err := s.zoneMgr.SetAgentState(state.State)
	if err != nil {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.Unavailable, err.Error())
	}

	if !slices.Contains(agent.ValidModes, state.Mode) {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Invalid mode:%s", state.Mode))
	}
	err = s.zoneMgr.SetAgentMode(state.Mode)
	if err != nil {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.Unavailable, err.Error())
	}

	err = s.zoneMgr.SetZoomEnable(state.ZoomEnable)
	if err != nil {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.Unavailable, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}
