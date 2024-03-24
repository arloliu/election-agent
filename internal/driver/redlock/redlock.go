package redlock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	time "time"

	"github.com/hashicorp/go-multierror"
)

type RedLock struct {
	conns  []Conn
	quorum int

	mu sync.Mutex
}

func New(conns ...Conn) *RedLock {
	return &RedLock{conns: conns, quorum: len(conns)/2 + 1}
}

func (r *RedLock) NewMutex(name string, value string, options ...Option) *Mutex {
	mutex := &Mutex{
		name:          name,
		expiry:        5 * time.Second,
		tries:         8,
		delayFunc:     defDelayFun,
		value:         value,
		randomValue:   false,
		driftFactor:   0.01,
		timeoutFactor: 0.05,
		quorum:        r.quorum,
		conns:         r.conns,
	}
	for _, opt := range options {
		opt.Apply(mutex)
	}

	return mutex
}

func (r *RedLock) GetConns() ([]Conn, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.conns, r.quorum
}

func (r *RedLock) SetConns(conns []Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.conns = conns
	r.quorum = len(r.conns)/2 + 1
}

func (r *RedLock) Ping(ctx context.Context) (int, error) {
	return actStatusOpAsync(r.conns, r.quorum, func(conn Conn) (bool, error) {
		return conn.NewWithContext(ctx).Ping()
	})
}

func (r *RedLock) Get(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", errors.New("The redis key is empty")
	}

	_, val, err := actStringOpAsync(r.conns, r.quorum, func(conn Conn) (string, error) {
		return conn.NewWithContext(ctx).Get(key)
	})

	return val, err
}

func (r *RedLock) Set(ctx context.Context, key string, value string) (int, error) {
	if key == "" {
		return 0, errors.New("Set: the key is empty")
	}

	return actStatusOpAsync(r.conns, r.quorum, func(conn Conn) (bool, error) {
		return conn.NewWithContext(ctx).Set(key, value)
	})
}

func (r *RedLock) SetBool(ctx context.Context, key string, value bool) (int, error) {
	if key == "" {
		return 0, errors.New("SetBool: the key for is empty")
	}
	return actStatusOpAsync(r.conns, r.quorum, func(conn Conn) (bool, error) {
		return conn.NewWithContext(ctx).Set(key, BoolStr(value))
	})
}

func (r *RedLock) MGet(ctx context.Context, keys ...string) ([]string, error) {
	type result struct {
		reply []string
		err   error
	}

	connSzie := len(r.conns)

	ch := make(chan result, connSzie)
	for _, conn := range r.conns {
		go func(conn Conn) {
			r := result{}
			r.reply, r.err = conn.NewWithContext(ctx).MGet(keys...)
			ch <- r
		}(conn)
	}

	n := 0
	var err error
	replies := make([][]string, len(keys))
	for i := 0; i < len(keys); i++ {
		replies[i] = make([]string, connSzie)
	}

	for i := 0; i < connSzie; i++ {
		r := <-ch
		if r.err == nil {
			for j := 0; j < len(keys); j++ {
				replies[j][i] = r.reply[j]
			}
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		}
	}

	// fast fail
	if n < r.quorum {
		return []string{}, fmt.Errorf("Get fails, the number of success operations %d < %d quorum, error: %w", n, r.quorum, err)
	}

	vals := make([]string, len(keys))
	for i, reply := range replies {
		var n int
		vals[i], n = getMostFreqVal(reply)
		if n < r.quorum {
			return []string{}, fmt.Errorf("Get fails, the number of the same (%s:%s) is %d < %d quorum, error: %w", keys[i], vals[i], n, r.quorum, err)
		}
	}

	return vals, nil
}

func (r *RedLock) MSet(ctx context.Context, pairs ...any) (int, error) {
	if len(pairs) == 0 {
		return 0, errors.New("MSet: the pairs is empty")
	}
	if len(pairs)%2 != 0 {
		return 0, errors.New("MSet: the length of pairs is not an odd number")
	}

	return actStatusOpAsync(r.conns, r.quorum, func(conn Conn) (bool, error) {
		return conn.NewWithContext(ctx).MSet(pairs...)
	})
}

type stringFunc func(conn Conn) (string, error)

func actStringOpAsync(conns []Conn, quorum int, actFunc stringFunc) (int, string, error) {
	type result struct {
		reply string
		err   error
	}

	connSize := len(conns)

	ch := make(chan result, connSize)
	for _, conn := range conns {
		go func(conn Conn) {
			r := result{}
			r.reply, r.err = actFunc(conn)
			ch <- r
		}(conn)
	}

	n := 0
	replies := make([]string, 0, connSize)
	var err error

	for i := 0; i < connSize; i++ {
		r := <-ch
		if r.err == nil {
			replies = append(replies, r.reply)
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		}
	}

	val, n := getMostFreqVal[string](replies)
	if n < quorum {
		return n, val, fmt.Errorf("Get fails, the number of the same value:%s is %d < %d quorum, error: %w", val, n, quorum, err)
	}

	return n, val, nil
}

type statusFunc func(conn Conn) (bool, error)

func actStatusOpAsync(conns []Conn, quorum int, actFunc statusFunc) (int, error) {
	type result struct {
		node     int
		statusOK bool
		err      error
	}

	connSize := len(conns)

	ch := make(chan result, connSize)
	for node, conn := range conns {
		go func(node int, conn Conn) {
			r := result{node: node}
			r.statusOK, r.err = actFunc(conn)
			ch <- r
		}(node, conn)
	}

	n := 0
	var taken []int
	var err error

	for i := 0; i < connSize; i++ {
		r := <-ch
		if r.statusOK { //nolint:gocritic
			n++
		} else if errors.Is(r.err, ErrLockAlreadyExpired) {
			err = multierror.Append(err, ErrLockAlreadyExpired)
		} else if r.err != nil {
			err = multierror.Append(err, &RedisError{Node: r.node, Err: r.err})
		} else {
			taken = append(taken, r.node)
			err = multierror.Append(err, &ErrNodeTaken{Node: r.node})
		}

		if n >= quorum {
			return n, err
		}

		if len(taken) >= quorum {
			return n, &ErrTaken{Nodes: taken}
		}
	}

	if len(taken) >= quorum {
		return n, &ErrTaken{Nodes: taken}
	}

	if n < quorum {
		return n, fmt.Errorf("The number of the failed operations %d >= %d quorum, error: %w", n, quorum, err)
	}

	return n, nil
}

func BoolStr(val bool) string {
	if val {
		return "1"
	} else {
		return "0"
	}
}

func IsBoolStrTrue(val string) bool {
	return val == "1"
}

type Option interface {
	Apply(*Mutex)
}

// OptionFunc is a function that configures a mutex.
type OptionFunc func(*Mutex)

func (f OptionFunc) Apply(mutex *Mutex) {
	f(mutex)
}

// WithExpiry can be used to set the expiry of a mutex to the given value.
// The default is 5s.
func WithExpiry(expiry time.Duration) Option {
	return OptionFunc(func(m *Mutex) {
		m.expiry = expiry
	})
}

// WithTries can be used to set the number of times lock acquire is attempted.
// The default value is 8.
func WithTries(tries int) Option {
	return OptionFunc(func(m *Mutex) {
		m.tries = tries
	})
}

// WithRetryDelay can be used to set the amount of time to wait between retries.
// The default value is rand(50ms, 250ms).
func WithRetryDelay(delay time.Duration) Option {
	return OptionFunc(func(m *Mutex) {
		m.delayFunc = func(tries int) time.Duration {
			return delay
		}
	})
}

// WithDriftFactor can be used to set the clock drift factor.
// The default value is 0.01.
func WithDriftFactor(factor float64) Option {
	return OptionFunc(func(m *Mutex) {
		m.driftFactor = factor
	})
}

// WithTimeoutFactor can be used to set the timeout factor.
// The default value is 0.05.
func WithTimeoutFactor(factor float64) Option {
	return OptionFunc(func(m *Mutex) {
		m.timeoutFactor = factor
	})
}

func getMostFreqVal[K comparable](vals []K) (K, int) {
	freqMap := make(map[K]int)
	for _, val := range vals {
		freqMap[val] += 1
	}

	// Find the value with the highest frequency
	var mostFreqVal K
	highestFreq := 0
	for val, frequency := range freqMap {
		if frequency > highestFreq {
			highestFreq = frequency
			mostFreqVal = val
		}
	}

	return mostFreqVal, highestFreq
}
