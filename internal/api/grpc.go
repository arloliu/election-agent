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
		return &pb.CampaignResult{}, status.Error(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Candidate == "" {
		return &pb.CampaignResult{}, status.Error(codes.InvalidArgument, "Empty field 'candidate'")
	}
	if req.Term < 1000 {
		return &pb.CampaignResult{}, status.Error(codes.InvalidArgument, "The field 'term' must >= 1000")
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
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'leader'")
	}
	if req.Term < 1000 {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "The field 'term' must >= 1000")
	}

	if req.Retries < 0 || req.Retries > 10 {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "The field 'retres' must in 0 ~ 10")
	}

	if req.RetryInterval < 0 || req.RetryInterval > 1000 {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "The field 'retry_interval' must in 0 ~ 1000")
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
			return &pb.BoolValue{Value: false}, status.Error(codes.AlreadyExists, err.Error())
		} else if lease.IsNonexistError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.NotFound, err.Error())
		}

		return &pb.BoolValue{Value: false}, nil
	}
	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) Resign(ctx context.Context, req *pb.ResignRequest) (*pb.BoolValue, error) {
	if req.Election == "" {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'leader'")
	}

	err := s.leaseMgr.RevokeLease(ctx, req.Election, req.Kind, req.Leader)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.BoolValue{Value: false}, nil
		}
		return &pb.BoolValue{Value: false}, status.Error(codes.NotFound, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) Handover(ctx context.Context, req *pb.HandoverRequest) (*pb.BoolValue, error) {
	if req.Election == "" {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'election'")
	}
	if req.Leader == "" {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "Empty field 'leader'")
	}
	if req.Term < 1000 {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, "The field 'term' must >= 1000")
	}

	err := s.leaseMgr.HandoverLease(ctx, req.Election, req.Kind, req.Leader, time.Duration(req.Term)*time.Millisecond)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.BoolValue{Value: false}, nil
		}
		return &pb.BoolValue{Value: false}, status.Error(codes.Unknown, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}

func (s *ElectionGRPCService) GetLeader(ctx context.Context, req *pb.GetLeaderRequest) (*pb.StringValue, error) {
	if req.Election == "" {
		return &pb.StringValue{Value: ""}, status.Error(codes.InvalidArgument, "Empty field 'election'")
	}

	leader, err := s.leaseMgr.GetLeaseHolder(ctx, req.Election, req.Kind)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.StringValue{Value: ""}, status.Error(codes.FailedPrecondition, err.Error())
		} else if lease.IsAgentStandbyError(err) {
			return &pb.StringValue{Value: ""}, nil
		}
		return &pb.StringValue{Value: ""}, status.Error(codes.NotFound, err.Error())
	}

	return &pb.StringValue{Value: leader}, nil
}

func (s *ElectionGRPCService) ListLeaders(ctx context.Context, req *pb.ListLeadersRequest) (*pb.Leaders, error) {
	holders, err := s.leaseMgr.GetLeaseHolders(ctx, req.Kind)
	if err != nil {
		if lease.IsUnavailableError(err) {
			return &pb.Leaders{Leaders: []*pb.Leader{}}, status.Error(codes.FailedPrecondition, err.Error())
		}
		return &pb.Leaders{Leaders: []*pb.Leader{}}, status.Error(codes.Unknown, err.Error())
	}

	leaders := make([]*pb.Leader, len(holders))
	for i, holder := range holders {
		leaders[i] = &pb.Leader{Election: holder.Election, Name: holder.Name}
	}
	return &pb.Leaders{Leaders: leaders}, nil
}

func (s *ElectionGRPCService) GetPods(ctx context.Context, req *pb.GetPodsRequest) (*pb.Pods, error) {
	if !s.cfg.Kube.Enable || s.kubeClient == nil {
		return nil, status.Error(codes.Unimplemented, "method GetPods not implemented")
	}
	pods, err := s.kubeClient.GetPods(req.Namespace, req.Deployment, req.PodName)
	if err != nil {
		return pods, status.Error(codes.NotFound, err.Error())
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
	st := agent.GetLocalStatus()
	return st.Proto(), nil
}

func (s *ControlGRPCService) SetStatus(ctx context.Context, as *pb.AgentStatus) (*pb.BoolValue, error) {
	if !slices.Contains(agent.ValidStates, as.State) {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, fmt.Sprintf("Invalid state:%s", as.State))
	}

	if !slices.Contains(agent.ValidModes, as.Mode) {
		return &pb.BoolValue{Value: false}, status.Error(codes.InvalidArgument, fmt.Sprintf("Invalid mode:%s", as.Mode))
	}

	st := agent.GetLocalStatus()
	st.State = as.State
	st.Mode = as.Mode
	err := s.zoneMgr.SetAgentStatus(&st)
	if err != nil {
		return &pb.BoolValue{Value: false}, status.Error(codes.FailedPrecondition, err.Error())
	}

	return &pb.BoolValue{Value: true}, nil
}
