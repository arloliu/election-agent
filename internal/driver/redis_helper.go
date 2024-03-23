package driver

import (
	"errors"
	"net"

	"election-agent/internal/driver/redlock"

	"github.com/hashicorp/go-multierror"
)

func getMultiError(err error) *multierror.Error {
	merr := &multierror.Error{}
	if !errors.As(err, &merr) {
		return nil
	}
	switch v := err.(type) { //nolint:errorlint
	case *multierror.Error:
		return v
	default:
		e := errors.Unwrap(err)
		if e == nil {
			return nil
		}
		merr, ok := e.(*multierror.Error) //nolint:errorlint
		if !ok {
			return nil
		}
		return merr
	}
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

	redisErr := &redlock.RedisError{}
	// check if error is a wrapped network operation error
	if errors.As(err, &redisErr) {
		rerr, ok := err.(*redlock.RedisError) //nolint:errorlint
		if !ok {
			return false
		}
		if errors.As(rerr.Err, &opErr) {
			return true
		}
	}

	return false
}
