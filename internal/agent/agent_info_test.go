package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentInfo_State(t *testing.T) {
	require := require.New(t)

	s := NewState(ActiveState, 100*time.Millisecond)
	require.NotNil(s)
	require.Equal(ActiveState, s.Load())
	require.False(s.Expired())

	s.Store(StandbyState)
	require.Equal(StandbyState, s.Load())

	s.Store(UnavailableState)
	require.Equal(UnavailableState, s.Load())

	time.Sleep(110 * time.Millisecond)
	require.True(s.Expired())
	require.Equal(UnavailableState, s.Load())

	s.Store(StandbyState)
	require.False(s.Expired())
	require.Equal(StandbyState, s.Load())
}
