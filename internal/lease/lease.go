package lease

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Lease interface {
	ID() uint64
	Grant(ctx context.Context) error
	Revoke(ctx context.Context) error
	Extend(ctx context.Context) error
}

type KVDriver interface {
	LeaseID(name string, holder string, ttl time.Duration) uint64
	NewLease(name string, holder string, ttl time.Duration) Lease
	GetHolder(ctx context.Context, name string) (string, error)
	Shutdown(ctx context.Context) error

	// GetAgentState gets agent state.
	GetAgentMode() (string, error)
	// SetAgentMode sets agent mode.
	SetAgentMode(mode string) error
	// Get the string value of key.
	Get(ctx context.Context, key string, strict bool) (string, error)
	// GetBool gets the boolean value of key.
	GetBool(ctx context.Context, key string, strict bool) (bool, error)
	// Set the string value of key.
	Set(ctx context.Context, key string, value string, strict bool) (int, error)
	// SetBool sets the boolean value of key.
	SetBool(ctx context.Context, key string, value bool, strict bool) (int, error)
}

type UnavailableError struct {
	Err error
}

func (e UnavailableError) Error() string {
	return fmt.Errorf("service unavailable, error: %w", e.Err).Error()
}

func IsUnavailableError(err error) bool {
	uerr := &UnavailableError{}
	return errors.As(err, &uerr)
}

var (
	ErrServiceUnavalable = &UnavailableError{Err: errors.New("service unavailable")}
	ErrAgentStandby      = errors.New("agent is in standby mode")
)
