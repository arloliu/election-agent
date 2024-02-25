package lease

import (
	"errors"
	"net"

	"github.com/go-redsync/redsync/v4"
	"github.com/hashicorp/go-multierror"
)

func isAllRedisDown(err error, redisCount int) bool {
	if err == nil {
		return false
	}

	merr := getMultiError(err)
	if merr == nil {
		return false
	}

	if merr.Len() < redisCount {
		return false
	}

	n := 0
	for _, err := range merr.Errors {
		if isNetOpError(err) {
			n++
		}
	}

	return n == redisCount
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

	redisErr := &redsync.RedisError{}
	opErr := &net.OpError{}
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
