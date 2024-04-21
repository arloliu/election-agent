package redlock

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var pseudo = rand.New(rand.NewSource(time.Now().UnixNano()))

func TestConn_SlotDistribution(t *testing.T) {
	require := require.New(t)

	connSize := 5
	keyCount := 100000
	occurrences := make([]int, connSize)
	for i := 0; i < keyCount; i++ {
		key := randStr(16)
		slot := connIdx(key, connSize)
		occurrences[slot]++
	}
	for i := 0; i < connSize-1; i++ {
		for j := 1; j < connSize; j++ {
			require.InDelta(occurrences[i], occurrences[j], float64(keyCount)/100)
		}
	}
}

var letterRunes = []rune("1234567890_-/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randStr(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[pseudo.Intn(len(letterRunes))]
	}
	return string(b)
}
