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

	result, err := s.leaseMgr.GrantLease(ctx, req.Election, req.Kind, req.Candidate, time.Duration(int64(req.Term)*int64(time.Millisecond)))
	campResult := &pb.CampaignResult{Elected: false, Leader: result.Holder, Kind: result.Kind}
	if err != nil {
		if lease.IsUnavailableError(err) {
			return campResult, status.Error(codes.FailedPrecondition, err.Error())
		}
		return campResult, nil
	}

	campResult.Elected = true
	return campResult, nil
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

	if req.Retries < 0 || req.Retries > 10 {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "The field 'retres' must in 0 ~ 10")
	}

	if req.RetryInterval < 0 || req.RetryInterval > 1000 {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "The field 'retry_interval' must in 0 ~ 1000")
	}

	retryInterval := time.Duration(req.RetryInterval) * time.Millisecond

	var err error
	for i := 0; i <= int(req.Retries); i++ {
		err = s.leaseMgr.ExtendLease(ctx, req.Election, req.Kind, req.Leader, time.Duration(req.Term)*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(retryInterval)
	}

	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsTakenError(err) {
			return &pb.BoolValue{Value: false}, status.Errorf(codes.AlreadyExists, err.Error())
		} else if lease.IsNonexistError(err) {
			return &pb.BoolValue{Value: false}, status.Errorf(codes.NotFound, err.Error())
		}

		return &pb.BoolValue{Value: false}, nil
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

	err := s.leaseMgr.RevokeLease(ctx, req.Election, req.Kind, req.Leader)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.BoolValue{Value: false}, nil
		}
		return &pb.BoolValue{Value: false}, status.Errorf(codes.NotFound, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) Handover(ctx context.Context, req *pb.HandoverRequest) (*pb.BoolValue, error) {
	if req.Election == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "Empty field 'leader'")
	}
	if req.Term < 1000 {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, "The field 'term' must >= 1000")
	}

	err := s.leaseMgr.HandoverLease(ctx, req.Election, req.Kind, req.Leader, time.Duration(req.Term)*time.Millisecond)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.BoolValue{Value: false}, nil
		}
		return &pb.BoolValue{Value: false}, status.Errorf(codes.Unknown, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) GetLeader(ctx context.Context, req *pb.GetLeaderRequest) (*pb.StringValue, error) {
	if req.Election == "" {
		return &pb.StringValue{Value: ""}, status.Errorf(codes.InvalidArgument, "Empty field 'election'")
	}

	leader, err := s.leaseMgr.GetLeaseHolder(ctx, req.Election, req.Kind)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.StringValue{Value: ""}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.StringValue{Value: ""}, nil
		}
		return &pb.StringValue{Value: ""}, status.Errorf(codes.NotFound, err.Error())
	}

	return &pb.StringValue{Value: leader}, nil
}

func (s *ElectionGRPCService) GetPods(ctx context.Context, req *pb.GetPodsRequest) (*pb.Pods, error) {
	if !s.cfg.Kube.Enable || s.kubeClient == nil {
		return nil, status.Errorf(codes.Unimplemented, "method GetPods not implemented")
	}
	pods, err := s.kubeClient.GetPods(req.Namespace, req.Deployment)
	if err != nil {
		return pods, status.Errorf(codes.NotFound, err.Error())
	}

	return pods, err
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
	st, err := s.zoneMgr.GetAgentStatus()
	if err != nil {
		return &pb.AgentStatus{State: agent.UnavailableState, Mode: agent.UnknownMode}, nil
	}

	return &pb.AgentStatus{State: st.State, Mode: st.Mode, ZoneEnable: st.ZoneEnable}, nil
}

func (s *ControlGRPCService) SetStatus(ctx context.Context, as *pb.AgentStatus) (*pb.BoolValue, error) {
	if !slices.Contains(agent.ValidStates, as.State) {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Invalid state:%s", as.State))
	}

	if !slices.Contains(agent.ValidModes, as.Mode) {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Invalid mode:%s", as.Mode))
	}

	err := s.zoneMgr.SetAgentStatus(&agent.Status{State: as.State, Mode: as.Mode, ZoneEnable: as.ZoneEnable})
	if err != nil {
		return &pb.BoolValue{Value: false}, status.Errorf(codes.FailedPrecondition, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}
