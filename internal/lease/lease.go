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
	Kind() string
	Grant(ctx context.Context) error
	Revoke(ctx context.Context) error
	Extend(ctx context.Context) error
	Handover(ctx context.Context, holder string) error
}

type KVDriver interface {
	LeaseID(name string, kind string, holder string, ttl time.Duration) uint64
	NewLease(name string, kind string, holder string, ttl time.Duration) Lease
	GetHolder(ctx context.Context, name string, kind string) (string, error)
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
	Get(ctx context.Context, key string) (string, error)
	// GetSetBool gets the boolean value of key, sets and returns defVal is the key doesn't exist.
	GetSetBool(ctx context.Context, key string, defVal bool) (bool, error)
	// MGet gets the string values of all specified keys
	MGet(ctx context.Context, keys ...string) ([]string, error)
	// Set the string value of key.
	Set(ctx context.Context, key string, value string) (int, error)
	// SetBool sets the boolean value of key.
	SetBool(ctx context.Context, key string, value bool) (int, error)
	// MSet sets the given keys to their respective values
	MSet(ctx context.Context, pairs ...any) (int, error)
}

type UnavailableError struct {
	Err error
}

func (e UnavailableError) Error() string {
	return e.Err.Error()
}

type AgentStandbyError struct {
	Err error
}

func (e AgentStandbyError) Error() string {
	return e.Err.Error()
}

type TakenError struct {
	Nodes []int
}

func (e TakenError) Error() string {
	return fmt.Sprintf("lease already taken, locked nodes: %v", e.Nodes)
}

type NonexistError struct {
	Lease string
}

func (e NonexistError) Error() string {
	return fmt.Errorf("The lease %s doesn't exist", e.Lease).Error()
}

type ExtendFailError struct {
	Lease string
	Err   error
}

func (e ExtendFailError) Error() string {
	return fmt.Errorf("Failed to extend lease %s, got error: %w", e.Lease, e.Err).Error()
}

type HandoverFailError struct {
	Lease  string
	Holder string
	Err    error
}

func (e HandoverFailError) Error() string {
	return fmt.Errorf("Failed to handover lease %s to %s, got error: %w", e.Lease, e.Holder, e.Err).Error()
}

func IsUnavailableError(err error) bool {
	uerr := &UnavailableError{}
	return errors.As(err, &uerr)
}

func IsAgentStandbyError(err error) bool {
	uerr := &AgentStandbyError{}
	return errors.As(err, &uerr)
}

func IsTakenError(err error) bool {
	uerr := &TakenError{}
	return errors.As(err, &uerr)
}

func IsNonexistError(err error) bool {
	uerr := &NonexistError{}
	return errors.As(err, &uerr)
}

func IsExtendFailError(err error) bool {
	uerr := &ExtendFailError{}
	return errors.As(err, &uerr)
}

func IsHandoverFailError(err error) bool {
	uerr := &HandoverFailError{}
	return errors.As(err, &uerr)
}

var (
	ErrServiceUnavalable = &UnavailableError{Err: errors.New("service unavailable")}
	ErrAgentStandby      = &AgentStandbyError{Err: errors.New("agent is in standby mode")}
)
