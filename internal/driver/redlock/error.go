package redlock

import (
	"errors"
	"fmt"
)

// ErrFailed is the error resulting if redlock fails to acquire the lock after exhausting all retries.
var ErrFailed = errors.New("redlock: failed to acquire lock")

// ErrExtendFailed is the error resulting if redlock fails to extend the lock.
var ErrExtendFailed = errors.New("redlock: failed to extend lock")

// ErrHandoverFailed is the error resulting if redlock fails to assign a new value to the current lock.
var ErrHandoverFailed = errors.New("redlock: failed to handover")

// ErrLockAlreadyExpired is the error resulting if trying to unlock the lock which already expired.
var ErrLockAlreadyExpired = errors.New("redlock: failed to unlock, lock was already expired")

// TakenError happens when the lock is already taken in a quorum on nodes.
type TakenError struct {
	Nodes []int
}

func (err TakenError) Error() string {
	return fmt.Sprintf("lock already taken, locked nodes: %v", err.Nodes)
}

// NodeTakenError is the error resulting if the lock is already taken in one of
// the cluster's nodes
type NodeTakenError struct {
	Node int
}

func (err *NodeTakenError) Error() string {
	return fmt.Sprintf("node #%d: lock already taken", err.Node)
}

// A RedisError is an error communicating with one of the Redis nodes.
type RedisError struct {
	Node int
	Err  error
}

func (err *RedisError) Error() string {
	return fmt.Sprintf("node #%d: %s", err.Node, err.Err)
}

func (err *RedisError) Unwrap() error {
	if err == nil || err.Err == nil {
		return nil
	}
	return err.Err
}
