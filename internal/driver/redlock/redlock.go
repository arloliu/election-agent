package redlock

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	time "time"

	"github.com/hashicorp/go-multierror"
)

const ShuffleConnPoolSize = 10

type RedLock struct {
	connShards ConnShards
	quorum     int

	shuffleConnPool [ShuffleConnPoolSize]ConnShards
	mu              sync.Mutex
}

func New(connShards ...ConnGroup) *RedLock {
	lock := &RedLock{}
	lock.SetConnShards(connShards)
	return lock
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
		getConns:      r.getConns,
	}
	for _, opt := range options {
		opt.Apply(mutex)
	}

	opTimeout := time.Duration(int64(float64(mutex.expiry) * mutex.timeoutFactor))
	if opTimeout < 500*time.Millisecond {
		opTimeout = 500 * time.Millisecond
	} else if opTimeout > 1500*time.Millisecond {
		opTimeout = 1500 * time.Millisecond
	}
	mutex.opTimeout = opTimeout

	return mutex
}

func (r *RedLock) GetQuorum() int {
	return r.quorum
}

func (r *RedLock) GetAllConnCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, cg := range r.connShards {
		n += len(cg)
	}

	return n
}

func (r *RedLock) getAllConns() []Conn {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := rand.Intn(ShuffleConnPoolSize) //nolint:gosec
	connPool := r.shuffleConnPool[idx]
	conns := make([]Conn, 0, len(connPool)*len(connPool[0]))
	for _, connShards := range connPool {
		conns = append(conns, connShards...)
	}

	return conns
}

func (r *RedLock) getMasterConns() ([]Conn, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := rand.Intn(ShuffleConnPoolSize) //nolint:gosec
	connShards := r.shuffleConnPool[idx]

	return connShards[0], r.quorum
}

func (r *RedLock) getConns(key string) ([]Conn, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := rand.Intn(ShuffleConnPoolSize) //nolint:gosec
	connShards := r.shuffleConnPool[idx]
	if key == "" {
		return connShards[0], r.quorum
	}

	return connShards.Conns(key), r.quorum
}

func (r *RedLock) SetConnShards(connShards ConnShards) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.connShards = connShards
	r.quorum = len(r.connShards[0])/2 + 1

	for i := 0; i < ShuffleConnPoolSize; i++ {
		clonedConnShards := make(ConnShards, len(connShards))
		for i, connGroup := range connShards {
			clonedConnShards[i] = make(ConnGroup, len(connGroup))
			_ = copy(clonedConnShards[i], connGroup)
		}

		rand.Shuffle(len(r.connShards[0]), func(i, j int) {
			for idx := range clonedConnShards {
				clonedConnShards[idx][i], clonedConnShards[idx][j] = clonedConnShards[idx][j], clonedConnShards[idx][i]
			}
		})

		r.shuffleConnPool[i] = clonedConnShards
	}
}

func (r *RedLock) Ping(ctx context.Context) (int, error) {
	conns := r.getAllConns()
	return actStatusOpAsync(conns, (len(conns)/2)+1, false, func(conn Conn) (bool, error) {
		return conn.WithContext(ctx).Ping()
	})
}

func (r *RedLock) Get(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", errors.New("The redis key is empty")
	}

	conns, quorum := r.getConns(key)
	_, val, err := actStringOpAsync(conns, quorum, func(conn Conn) (string, error) {
		return conn.WithContext(ctx).Get(key)
	})

	return val, err
}

func (r *RedLock) Set(ctx context.Context, key string, value string) (int, error) {
	if key == "" {
		return 0, errors.New("Set: the key is empty")
	}

	conns, quorum := r.getConns(key)
	return actStatusOpAsync(conns, quorum, false, func(conn Conn) (bool, error) {
		return conn.WithContext(ctx).Set(key, value)
	})
}

func (r *RedLock) SetBool(ctx context.Context, key string, value bool) (int, error) {
	if key == "" {
		return 0, errors.New("SetBool: the key for is empty")
	}

	conns, quorum := r.getConns(key)
	return actStatusOpAsync(conns, quorum, false, func(conn Conn) (bool, error) {
		return conn.WithContext(ctx).Set(key, BoolStr(value))
	})
}

func (r *RedLock) MGet(ctx context.Context, keys ...string) ([]string, error) {
	type result struct {
		reply []string
		err   error
	}

	conns, quorum := r.getMasterConns()
	connSzie := len(conns)

	ch := make(chan result, connSzie)
	for _, conn := range conns {
		go func(conn Conn) {
			rs := result{}
			rs.reply, rs.err = conn.WithContext(ctx).MGet(keys...)
			ch <- rs
		}(conn)
	}

	n := 0
	replies := make([][]string, len(keys))
	var err error

	for i := 0; i < len(keys); i++ {
		replies[i] = make([]string, connSzie)
	}

	for i := 0; i < connSzie; i++ {
		rs := <-ch
		if rs.err == nil {
			n++
			for j := 0; j < len(keys); j++ {
				if rs.reply[j] != "" {
					replies[j][i] = rs.reply[j]
				}
			}
		} else {
			err = multierror.Append(err, rs.err)
		}
	}

	if n < quorum {
		return []string{}, fmt.Errorf("Get fails, the number of success operations %d < %d quorum, error: %w", n, r.quorum, err)
	}

	vals := make([]string, len(keys))
	for i, reply := range replies {
		val, n := getMostFreqVal(reply)
		if n >= r.quorum {
			vals[i] = val
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

	conns, quorum := r.getMasterConns()
	return actStatusOpAsync(conns, quorum, false, func(conn Conn) (bool, error) {
		return conn.WithContext(ctx).MSet(pairs...)
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
		rs := <-ch
		if rs.err == nil {
			replies = append(replies, rs.reply)
			n++
		} else if rs.err != nil {
			err = multierror.Append(err, rs.err)
		}
	}

	val, n := getMostFreqVal[string](replies)
	if n < quorum {
		return n, val, fmt.Errorf("Get fails, the number of the same value:%s is %d < %d quorum, error: %w", val, n, quorum, err)
	}

	return n, val, nil
}

type statusFunc func(conn Conn) (bool, error)

func actStatusOpAsync(conns []Conn, quorum int, failFast bool, actFunc statusFunc) (int, error) {
	type result struct {
		node     int
		statusOK bool
		err      error
	}

	connSize := len(conns)

	ch := make(chan result, connSize)
	for node, conn := range conns {
		go func(node int, conn Conn) {
			rs := result{node: node}
			rs.statusOK, rs.err = actFunc(conn)
			ch <- rs
		}(node, conn)
	}

	n := 0
	var taken []int
	var err error

	for i := 0; i < connSize; i++ {
		rs := <-ch
		if rs.statusOK {
			n++
		} else if rs.err != nil {
			if errors.Is(rs.err, ErrLockAlreadyExpired) {
				err = multierror.Append(err, ErrLockAlreadyExpired)
			} else {
				err = multierror.Append(err, &RedisError{Node: rs.node, Err: rs.err})
			}
		} else {
			taken = append(taken, rs.node)
			err = multierror.Append(err, &NodeTakenError{Node: rs.node})
		}

		if failFast {
			if len(taken) >= quorum {
				return n, &TakenError{Nodes: taken}
			}
		}
	}

	if len(taken) >= quorum {
		return n, &TakenError{Nodes: taken}
	}

	if n < quorum {
		return n, fmt.Errorf("The number of success operations %d < %d quorum, taken:%d, error: %w", n, quorum, len(taken), err)
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
	freqMap := make(map[K]int, 1)
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
