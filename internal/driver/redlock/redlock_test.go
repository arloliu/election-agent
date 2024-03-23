package redlock

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

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
