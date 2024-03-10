package main

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	eagrpc "election-agent/proto/election_agent/v1"
)

type grpcClient struct {
	Conn     *grpc.ClientConn
	Election eagrpc.ElectionClient
	Control  eagrpc.ControlClient
}

func newGrpcClient(ctx context.Context, host string) (*grpcClient, error) {
	var err error
	c := &grpcClient{}
	c.Conn, err = grpc.DialContext(ctx, host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
	)
	if err != nil {
		return nil, err
	}
	c.Election = eagrpc.NewElectionClient(c.Conn)
	c.Control = eagrpc.NewControlClient(c.Conn)
	return c, nil
}

func marshalJSON(v protoreflect.ProtoMessage) (string, error) {
	codec := protojson.MarshalOptions{EmitUnpopulated: true}
	b, err := codec.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
