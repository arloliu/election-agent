package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"election-agent/internal/agent"
)

type Lease interface {
	ID() uint64
	Grant(ctx context.Context) error
	Revoke(ctx context.Context) error
	Extend(ctx context.Context) error
}

type Mutex interface {
	Lock(ctx context.Context) error
	Unlock(tx context.Context) (bool, error)
}

type KVDriver interface {
	LeaseID(name string, holder string, ttl time.Duration) uint64
	NewLease(name string, holder string, ttl time.Duration) Lease
	NewMutex(name string, ttl time.Duration) Mutex
	GetHolder(ctx context.Context, name string) (string, error)
	Shutdown(ctx context.Context) error

	// GetAgentMode gets agent mode.
	GetAgentMode() (string, error)
	// SetAgentMode sets agent mode.
	SetAgentMode(mode string) error
	// GetAgentState gets agent state.
	GetAgentState() (string, error)
	// SetAgentState sets agent state.
	SetAgentState(state string) error

	GetAgentStatus() (*agent.Status, error)
	SetAgentStatus(status *agent.Status) error

	// SetOpearationMode sets the operation mode by agent mode
	SetOpearationMode(mode string)

	// Ping checks the backend status
	Ping(ctx context.Context) (int, error)
	// Get the string value of key.
	Get(ctx context.Context, key string, strict bool) (string, error)
	// GetSetBool gets the boolean value of key, sets and returns defVal is the key doesn't exist.
	GetSetBool(ctx context.Context, key string, defVal bool, strict bool) (bool, error)
	// MGet gets the string values of all specified keys
	MGet(ctx context.Context, keys ...string) ([]string, error)
	// Set the string value of key.
	Set(ctx context.Context, key string, value string, strict bool) (int, error)
	// SetBool sets the boolean value of key.
	SetBool(ctx context.Context, key string, value bool, strict bool) (int, error)
	// MSet sets the given keys to their respective values
	MSet(ctx context.Context, pairs ...any) (int, error)
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
