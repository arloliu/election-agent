package redlock

import (
	"context"
)

func (m *Mutex) acquire(ctx context.Context, conn Conn, value string) (bool, error) {
	reply, err := conn.NewWithContext(ctx).SetNX(m.name, value, m.expiry)
	if err != nil {
		return false, err
	}
	return reply, nil
}

var deleteScript = NewScript(1, `
	local val = redis.call("GET", KEYS[1])
	if val == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	elseif val == false then
		return -1
	else
		return 0
	end
`)

func (m *Mutex) release(ctx context.Context, conn Conn, value string) (bool, error) {
	status, err := conn.NewWithContext(ctx).Eval(deleteScript, m.name, value)
	if err != nil {
		return false, err
	}
	if status == int64(-1) {
		return false, ErrLockAlreadyExpired
	}
	return status != int64(0), nil
}

var touchScript = NewScript(1, `
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("PEXPIRE", KEYS[1], ARGV[2])
	elseif redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
		return 1
	else
		return 0
	end
`)

func (m *Mutex) touch(ctx context.Context, conn Conn, value string, expiry int) (bool, error) {
	status, err := conn.NewWithContext(ctx).Eval(touchScript, m.name, value, expiry)
	if err != nil {
		return false, err
	}
	return status != int64(0), nil
}
