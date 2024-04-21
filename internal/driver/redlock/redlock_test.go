package redlock

import (
	"math/rand"
	"testing"

	mock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func mockConnGet(t *testing.T, key string) *MockConn {
	mockConn := NewMockConn(t)
	mockConn.On("Get", mock.AnythingOfType("string")).Return(key, nil)
	return mockConn
}

func TestRedlock_shufflePool(t *testing.T) {
	require := require.New(t)

	connGroup1 := ConnGroup{mockConnGet(t, "c1"), mockConnGet(t, "c2"), mockConnGet(t, "c3"), mockConnGet(t, "c4"), mockConnGet(t, "c5")}
	connGroup2 := ConnGroup{mockConnGet(t, "c1"), mockConnGet(t, "c2"), mockConnGet(t, "c3"), mockConnGet(t, "c4"), mockConnGet(t, "c5")}
	connGroup3 := ConnGroup{mockConnGet(t, "c1"), mockConnGet(t, "c2"), mockConnGet(t, "c3"), mockConnGet(t, "c4"), mockConnGet(t, "c5")}
	connShards := ConnShards{connGroup1, connGroup2, connGroup3}
	rlock := New(connShards...)
	require.NotNil(rlock)

	for _, pool := range rlock.shuffleConnPool {
		for i := 0; i < len(pool[0]); i++ {
			key1, _ := pool[0][i].Get("")
			key2, _ := pool[1][i].Get("")
			key3, _ := pool[2][i].Get("")
			require.Equal(key1, key2)
			require.Equal(key2, key3)
		}
	}
}

func TestRedlock_getMostFreqVal(t *testing.T) {
	require := require.New(t)

	strs := make([]string, 0, 1000)
	for i := 0; i < 400; i++ {
		strs = append(strs, "item1")
	}
	for i := 0; i < 600; i++ {
		strs = append(strs, "item2")
	}

	rand.Shuffle(len(strs), func(i, j int) {
		strs[i], strs[j] = strs[j], strs[i]
	})

	val, freq := getMostFreqVal(strs)
	require.Equal("item2", val)
	require.Equal(600, freq)

	strs = make([]string, 0, 5)
	for i := 0; i < 3; i++ {
		strs = append(strs, "")
	}
	for i := 0; i < 2; i++ {
		strs = append(strs, "item")
	}

	val, freq = getMostFreqVal(strs)
	require.Equal("", val)
	require.Equal(3, freq)
}
