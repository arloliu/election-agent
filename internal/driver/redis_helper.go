package driver

import (
	"errors"
	"net"

	"github.com/go-redsync/redsync/v4"
	"github.com/hashicorp/go-multierror"
)

func boolStr(val bool) string {
	if val {
		return "true"
	} else {
		return "false"
	}
}

func isBoolStrTrue(val string) bool {
	return val == "true"
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
