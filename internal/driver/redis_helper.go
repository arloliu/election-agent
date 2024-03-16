package driver

import (
	context "context"
	"errors"
	"net"

	"github.com/go-redsync/redsync/v4"
	"github.com/hashicorp/go-multierror"
	redis "github.com/redis/go-redis/v9"
)

func boolStr(val bool) string {
	if val {
		return "1"
	} else {
		return "0"
	}
}

func isBoolStrTrue(val string) bool {
	return val == "1"
}

func getMultiError(err error) *multierror.Error {
	merr := &multierror.Error{}
	if !errors.As(err, &merr) {
		return nil
	}
	return err.(*multierror.Error) //nolint:errorlint
}

func isNetOpError(err error) bool {
	if err == nil {
		return false
	}

	opErr := &net.OpError{}
	// check if error is a network operation error
	if errors.As(err, &opErr) {
		return true
	}

	redisErr := &redsync.RedisError{}
	// check if error is a wrapped network operation error
	if errors.As(err, &redisErr) {
		rerr, ok := err.(*redsync.RedisError) //nolint:errorlint
		if !ok {
			return false
		}
		if errors.As(rerr.Err, &opErr) {
			return true
		}
	}

	return false
}

func noErrNil(err error) error {
	if err == redis.Nil { //nolint:errorlint
		return nil
	}
	return err
}

func getMostFreqVal[K comparable](vals []K) (K, int) {
	freqMap := make(map[K]int)
	for _, val := range vals {
		freqMap[val] += 1
	}

	// Find the value with the highest frequency
	var mostFreqVal K
	highestFreq := 0
	for val, frequency := range freqMap {
		if frequency > highestFreq {
			highestFreq = frequency
			mostFreqVal = val
		}
	}

	return mostFreqVal, highestFreq
}

func getConnFromPool(ctx context.Context, pool RedisPool) (RedisConn, error) {
	c, err := pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	conn, ok := c.(RedisConn)
	if !ok {
		return nil, errors.New("the connection is not a RedisConn interface")
	}
	return conn, nil
}
