package redlock

import (
	"context"
	"strconv"
	"time"
)

var acquireScript = NewScript(1, `
	-- acquire
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("PEXPIRE", KEYS[1], ARGV[2])
	elseif redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
		return 1
	else
		return 0
	end
`)

func (m *Mutex) acquire(ctx context.Context, conn Conn, value string) (bool, error) {
	reply, err := conn.Eval(ctx, acquireScript, []string{m.name}, []string{value, strconv.FormatInt(int64(m.expiry/time.Millisecond), 10)})
	if err != nil {
		return false, err
	}
	return reply != int64(0), nil
}

var deleteScript = NewScript(1, `
	-- delete
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
	status, err := conn.Eval(ctx, deleteScript, []string{m.name}, []string{value})
	if err != nil {
		return false, err
	}
	if status == int64(-1) {
		return false, ErrLockAlreadyExpired
	}
	return status != int64(0), nil
}

var touchScript = NewScript(1, `
	-- touch
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("PEXPIRE", KEYS[1], ARGV[2])
	elseif redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
		return 1
	else
		return 0
	end
`)

func (m *Mutex) touch(ctx context.Context, conn Conn, value string, expiry int64) (bool, error) {
	status, err := conn.Eval(ctx, touchScript, []string{m.name}, []string{value, strconv.FormatInt(expiry, 10)})
	if err != nil {
		return false, err
	}
	return status != int64(0), nil
}

var handoverScript = NewScript(1, `
	-- handover
	return redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
`)

func (m *Mutex) handover(ctx context.Context, conn Conn, value string, expiry int64) (bool, error) {
	status, err := conn.Eval(ctx, handoverScript, []string{m.name}, []string{value, strconv.FormatInt(expiry, 10)})
	if err != nil {
		return false, err
	}

	return status == "OK", nil
}
