package redlock

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"io"
	"time"
)

type RedisPool interface {
	Get(ctx context.Context) (Conn, error)
}

type Conn interface {
	WithContext(ctx context.Context) Conn
	Get(name string) (string, error)
	Set(name string, value string) (bool, error)
	SetNX(name string, value string, expiry time.Duration) (bool, error)
	Eval(script *Script, keysAndArgs ...any) (any, error)
	PTTL(name string) (time.Duration, error)
	Close(ctx context.Context) error
	Ping() (bool, error)
	MGet(keys ...string) ([]string, error)
	MSet(pairs ...any) (bool, error)
}

type Script struct {
	KeyCount int
	Src      string
	Hash     string
}

func NewScript(keyCount int, src string) *Script {
	h := sha1.New() //nolint:gosec
	_, _ = io.WriteString(h, src)
	return &Script{keyCount, src, hex.EncodeToString(h.Sum(nil))}
}
