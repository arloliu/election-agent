package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"election-agent/internal/agent"
	eagrpc "election-agent/proto/election_agent/v1"
)

func Test_marshalJSON(t *testing.T) {
	require := require.New(t)

	data := make([]protoreflect.ProtoMessage, 3)
	for i := 0; i < 3; i++ {
		data[i] = &eagrpc.AgentStatus{State: agent.ActiveState, Mode: agent.NormalMode, ZoneEnable: true}
	}

	ret, err := marshalJSON(data)
	require.NoError(err)
	require.NotEmpty(ret)

	var statues []*eagrpc.AgentStatus
	err = json.Unmarshal([]byte(ret), &statues)
	require.NoError(err)
	require.NotEmpty(statues)

	for _, status := range statues {
		require.Equal(agent.ActiveState, status.State)
		require.Equal(agent.NormalMode, status.Mode)
		require.True(status.ZoneEnable)
	}
}
