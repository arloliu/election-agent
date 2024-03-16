package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-redsync/redsync/v4"
	"github.com/hashicorp/go-multierror"
)

func (rd *RedisKVDriver) redisGetAsync(ctx context.Context, key string, strict bool) (string, error) {
	if key == "" {
		return "", errors.New("The redis key is empty")
	}

	type result struct {
		reply string
		err   error
	}

	poolSize := len(rd.pools)

	ch := make(chan result, poolSize)
	for _, pool := range rd.pools {
		go func(pool RedisPool) {
			r := result{}
			r.reply, r.err = rd.poolGet(ctx, pool, key)
			ch <- r
		}(pool)
	}

	n := 0
	replies := make([]string, 0, poolSize)
	var err error

	for i := 0; i < poolSize; i++ {
		r := <-ch
		if r.err == nil {
			replies = append(replies, r.reply)
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		}
	}

	val, n := getMostFreqVal[string](replies)
	if strict {
		if n < rd.quorum {
			return val, fmt.Errorf("Get fails, the number of the same (%s:%s) is %d < %d quorum, error: %w", key, val, n, rd.quorum, err)
		}
	} else if n == 0 || rd.isRedisUnhealthy(err) {
		return val, fmt.Errorf("Get %s fails, redis nodes are unhealty", key)
	}

	return val, nil
}

func (rd *RedisKVDriver) poolGet(ctx context.Context, pool RedisPool, key string) (string, error) {
	conn, err := getConnFromPool(ctx, pool)
	if err != nil {
		return "", err
	}

	return conn.Get(key)
}

func (rd *RedisKVDriver) redisMGetAsync(ctx context.Context, keys ...string) ([]string, error) {
	type result struct {
		reply []string
		err   error
	}

	poolSize := len(rd.pools)

	ch := make(chan result, poolSize)
	for _, pool := range rd.pools {
		go func(pool RedisPool) {
			r := result{}
			r.reply, r.err = rd.poolMGet(ctx, pool, keys)
			ch <- r
		}(pool)
	}

	n := 0
	var err error
	replies := make([][]string, len(keys))
	for i := 0; i < len(keys); i++ {
		replies[i] = make([]string, poolSize)
	}

	for i := 0; i < poolSize; i++ {
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
	if n < rd.quorum {
		return []string{}, fmt.Errorf("Get fails, the number of success operations %d < %d quorum, error: %w", n, rd.quorum, err)
	}

	vals := make([]string, len(keys))
	for i, reply := range replies {
		var n int
		vals[i], n = getMostFreqVal(reply)
		if n < rd.quorum {
			return []string{}, fmt.Errorf("Get fails, the number of the same (%s:%s) is %d < %d quorum, error: %w", keys[i], vals[i], n, rd.quorum, err)
		}
	}

	return vals, nil
}

func (rd *RedisKVDriver) poolMGet(ctx context.Context, pool RedisPool, keys []string) ([]string, error) {
	conn, err := getConnFromPool(ctx, pool)
	if err != nil {
		return nil, err
	}
	return conn.MGet(keys...)
}

type statusFunc func(pool RedisPool) (bool, error)

func (rd *RedisKVDriver) actStatusOpAsync(strict bool, actFunc statusFunc) (int, error) {
	type result struct {
		node     int
		statusOK bool
		err      error
	}

	poolSize := len(rd.pools)

	ch := make(chan result, poolSize)
	for node, pool := range rd.pools {
		go func(node int, pool RedisPool) {
			r := result{node: node}
			r.statusOK, r.err = actFunc(pool)
			ch <- r
		}(node, pool)
	}

	n := 0
	var taken []int
	var err error

	for i := 0; i < poolSize; i++ {
		r := <-ch
		if r.statusOK {
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		} else {
			taken = append(taken, r.node)
			err = multierror.Append(err, &redsync.ErrNodeTaken{Node: r.node})
		}
	}

	if strict {
		if len(taken) >= rd.quorum {
			return n, &redsync.ErrTaken{Nodes: taken}
		}

		if n < rd.quorum {
			return n, fmt.Errorf("The number of the failed operations %d >= %d quorum, error: %w", n, rd.quorum, err)
		}
	} else if n == 0 {
		return n, errors.New("All redis nodes are down")
	}

	return n, nil
}

func (rd *RedisKVDriver) isRedisUnhealthy(err error) bool {
	if err == nil {
		return false
	}

	merr := getMultiError(err)
	if merr == nil {
		return false
	}

	if merr.Len() < rd.quorum {
		return false
	}

	n := 0
	for _, err := range merr.Errors {
		if isNetOpError(err) {
			n++
		}
	}

	return n > len(rd.pools)-rd.quorum
}
