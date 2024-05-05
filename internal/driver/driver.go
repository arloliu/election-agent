package driver

import (
	"context"
	"time"

	"election-agent/internal/agent"
)

type Mutex interface {
	Name() string
	Value() string
	TryLockContext(ctx context.Context) error
	LockContext(ctx context.Context) error
	UnlockContext(ctx context.Context) (bool, error)
	ExtendContext(ctx context.Context) (bool, error)
	HandoverContext(ctx context.Context, holder string) (bool, error)
}

type KVDriver interface {
	NewMutex(name string, kind string, holder string, ttl time.Duration) Mutex

	ConnectionCount() int
	RebuildConnections() error
	Shutdown(ctx context.Context) error
	IsUnhealthy(err error) bool
	// SetOperationMode sets the operation mode by agent mode
	SetOperationMode(mode string)

	LeaseID(name string, kind string, holder string, ttl time.Duration) uint64
	GetHolder(ctx context.Context, name string, kind string) (string, error)
	GetHolders(ctx context.Context, kind string) (holders []Holder, err error)
	// GetActiveZone gets agent active zone.
	GetActiveZone() (string, error)
	// GetAgentState gets agent state.
	GetAgentState() (string, error)
	// GetAgentStatus gets the agent status including state, mode and zone enable status
	GetAgentStatus() (*agent.Status, error)
	// SetAgentStatus gets the agent status including state, mode and zone enable status
	SetAgentStatus(status *agent.Status) error

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

type Holder struct {
	Election string
	Name     string
}
