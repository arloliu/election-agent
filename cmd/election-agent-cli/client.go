package main

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	"election-agent/internal/config"
	eagrpc "election-agent/proto/election_agent/v1"
)

const ctxTimeout = 3 * time.Second

type grpcClient struct {
	Conn     *grpc.ClientConn
	Election eagrpc.ElectionClient
	Control  eagrpc.ControlClient
}

func newGrpcClient(ctx context.Context, host string) (*grpcClient, error) {
	var err error
	c := &grpcClient{}

	svcConfig := config.GrpcClientServiceConfig(ctxTimeout, 10, true)

	c.Conn, err = grpc.DialContext(ctx, host,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(svcConfig),
	)
	if err != nil {
		return nil, err
	}
	c.Election = eagrpc.NewElectionClient(c.Conn)
	c.Control = eagrpc.NewControlClient(c.Conn)
	return c, nil
}

func marshalProtoJSON(v protoreflect.ProtoMessage) (string, error) {
	codec := protojson.MarshalOptions{EmitUnpopulated: true}
	b, err := codec.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func parseInt[T int | int32](s string) (T, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return T(n), nil
}
