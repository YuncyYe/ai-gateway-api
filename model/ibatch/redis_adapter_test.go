// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License. All rights reserved.

package ibatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bfenetworks/bfe/bfe_util/redis_client"
	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRawRedisClient 手写 callback mock of bfe redis_client.Client。
type fakeRawRedisClient struct {
	scriptFn func(src string) redis_client.RedisScript
	deleteFn func(key string) error
}

func (f *fakeRawRedisClient) Setex(key string, value []byte, expire int) error {
	return nil
}
func (f *fakeRawRedisClient) Get(key string) (interface{}, error) { return nil, nil }
func (f *fakeRawRedisClient) HGetAll(key string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeRawRedisClient) Expire(key string, expire int) error { return nil }
func (f *fakeRawRedisClient) Incr(key string) (int64, error)      { return 0, nil }
func (f *fakeRawRedisClient) IncrAndExpire(key string, expire int) (int64, error) {
	return 0, nil
}
func (f *fakeRawRedisClient) Decr(key string) (int64, error) { return 0, nil }
func (f *fakeRawRedisClient) PIncr(keys []string) ([]int64, error) {
	return nil, nil
}
func (f *fakeRawRedisClient) GetInt64(key string) (int64, error) { return 0, nil }
func (f *fakeRawRedisClient) GetInt64Batch(keys []string) ([]int64, error) {
	return nil, nil
}
func (f *fakeRawRedisClient) IncrBy(key string, delta int64) (int64, error) { return 0, nil }
func (f *fakeRawRedisClient) Delete(key string) error {
	if f.deleteFn != nil {
		return f.deleteFn(key)
	}
	return nil
}
func (f *fakeRawRedisClient) NewScript(src string) redis_client.RedisScript {
	if f.scriptFn != nil {
		return f.scriptFn(src)
	}
	return &fakeRawScript{}
}

// fakeRawScript 记录 (key, args) 并返回 canned reply。
type fakeRawScript struct {
	scriptName string
	lastKey    string
	lastArgs   []interface{}
	reply      interface{}
	err        error
}

func (s *fakeRawScript) Run(key string, args ...interface{}) (interface{}, error) {
	s.lastKey = key
	s.lastArgs = args
	return s.reply, s.err
}

func TestRedisClientAdapter_LuaOps(t *testing.T) {
	t.Run("HGetAll 命中与 miss", func(t *testing.T) {
		script := &fakeRawScript{reply: []interface{}{[]byte("a"), []byte("1"), []byte("b"), []byte("2")}}
		a := NewRedisClientAdapter(&fakeRawRedisClient{scriptFn: func(src string) redis_client.RedisScript {
			assert.Contains(t, src, "HGETALL")
			return script
		}}, nil)

		vals, err := a.HGetAll(context.Background(), "BATCH_TASK:b-1")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"a": "1", "b": "2"}, vals)
		assert.Equal(t, "BATCH_TASK:b-1", script.lastKey)

		// 空 hash / nil reply → miss。
		script.reply = nil
		vals, err = a.HGetAll(context.Background(), "k")
		require.NoError(t, err)
		assert.Nil(t, vals)
	})

	t.Run("HGetAll 底层错误", func(t *testing.T) {
		script := &fakeRawScript{err: errors.New("redis down")}
		a := NewRedisClientAdapter(&fakeRawRedisClient{scriptFn: func(src string) redis_client.RedisScript {
			return script
		}}, nil)
		_, err := a.HGetAll(context.Background(), "k")
		require.Error(t, err)
	})

	t.Run("HSetWithTTL 参数编码", func(t *testing.T) {
		script := &fakeRawScript{}
		a := NewRedisClientAdapter(&fakeRawRedisClient{scriptFn: func(src string) redis_client.RedisScript {
			assert.Contains(t, src, "HSET")
			return script
		}}, nil)
		err := a.HSetWithTTL(context.Background(), "k", 48, map[string]string{"f1": "v1", "f2": "v2"})
		require.NoError(t, err)
		require.Len(t, script.lastArgs, 5)
		assert.Equal(t, 48, script.lastArgs[0])
	})

	t.Run("SetNXWithTTL 抢锁语义", func(t *testing.T) {
		script := &fakeRawScript{reply: "OK"}
		a := NewRedisClientAdapter(&fakeRawRedisClient{scriptFn: func(src string) redis_client.RedisScript {
			assert.Contains(t, src, "'NX'")
			return script
		}}, nil)
		ok, err := a.SetNXWithTTL(context.Background(), "k", 48)
		require.NoError(t, err)
		assert.True(t, ok)

		script.reply = nil // 已存在 → 非 OK
		ok, err = a.SetNXWithTTL(context.Background(), "k", 48)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("ReserveDecr 钳制语义", func(t *testing.T) {
		script := &fakeRawScript{reply: int64(7)}
		a := NewRedisClientAdapter(&fakeRawRedisClient{scriptFn: func(src string) redis_client.RedisScript {
			assert.Contains(t, src, "DECRBY")
			return script
		}}, nil)
		decr, err := a.ReserveDecr(context.Background(), "k", 10)
		require.NoError(t, err)
		assert.Equal(t, int64(7), decr)
		require.Equal(t, []interface{}{int64(10)}, script.lastArgs)
	})

	t.Run("Delete 与 nil client", func(t *testing.T) {
		var deleted string
		a := NewRedisClientAdapter(&fakeRawRedisClient{deleteFn: func(key string) error {
			deleted = key
			return nil
		}}, nil)
		require.NoError(t, a.Delete(context.Background(), "k"))
		assert.Equal(t, "k", deleted)

		nilAdapter := NewRedisClientAdapter(nil, nil)
		_, err := nilAdapter.HGetAll(context.Background(), "k")
		require.Error(t, err)
		_, err = nilAdapter.SetNXWithTTL(context.Background(), "k", 1)
		require.Error(t, err)
		require.Error(t, nilAdapter.HSetWithTTL(context.Background(), "k", 1, nil))
		_, err = nilAdapter.ReserveDecr(context.Background(), "k", 1)
		require.Error(t, err)
		require.Error(t, nilAdapter.Delete(context.Background(), "k"))
	})
}

type fakeScanner struct {
	fn func(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error)
}

func (f *fakeScanner) Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
	if f.fn != nil {
		return f.fn(ctx, cursor, match, count)
	}
	return 0, nil, nil
}

func TestRedisClientAdapter_Scan(t *testing.T) {
	t.Run("nil scanner 降级为空", func(t *testing.T) {
		a := NewRedisClientAdapter(&fakeRawRedisClient{}, nil)
		next, keys, err := a.Scan(context.Background(), 0, "BATCH_TASK:*", 200)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), next)
		assert.Nil(t, keys)
	})

	t.Run("透传 scanner", func(t *testing.T) {
		a := NewRedisClientAdapter(&fakeRawRedisClient{}, &fakeScanner{fn: func(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
			assert.Equal(t, "BATCH_TASK:*", match)
			assert.Equal(t, int64(200), count)
			return 0, []string{"BATCH_TASK:a"}, nil
		}})
		next, keys, err := a.Scan(context.Background(), 0, "BATCH_TASK:*", 200)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), next)
		assert.Equal(t, []string{"BATCH_TASK:a"}, keys)
	})
}

// fakeScanConn 实现 redis.Conn，仅应答 SCAN 命令（多轮游标）。
type fakeScanConn struct {
	t        *testing.T
	cursors  []uint64 // 依次返回的游标；最后一个必须为 0
	calls    int
	lastArgs []interface{}
	closed   bool
}

func (c *fakeScanConn) Close() error { c.closed = true; return nil }
func (c *fakeScanConn) Err() error   { return nil }
func (c *fakeScanConn) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	if commandName != "SCAN" {
		return nil, errors.New("unexpected command: " + commandName)
	}
	idx := c.calls
	if idx >= len(c.cursors) {
		idx = len(c.cursors) - 1
	}
	c.calls++
	cursor := c.cursors[idx]
	return []interface{}{[]byte(itoa(cursor)), []interface{}{[]byte("BATCH_TASK:k")}}, nil
}
func (c *fakeScanConn) DoWithTimeout(timeout time.Duration, commandName string, args ...interface{}) (interface{}, error) {
	return c.Do(commandName, args...)
}
func (c *fakeScanConn) Send(commandName string, args ...interface{}) error { return nil }
func (c *fakeScanConn) Flush() error                                       { return nil }
func (c *fakeScanConn) Receive() (interface{}, error)                      { return nil, nil }
func (c *fakeScanConn) ReceiveWithTimeout(time.Duration) (interface{}, error) {
	return nil, nil
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestRedigoScanner_ConstructAndClose(t *testing.T) {
	// 构造只建池不拨号（惰性），地址不可达也不影响。
	s := NewRedigoScanner([]string{"127.0.0.1:6390", "127.0.0.1:6391"}, "pw", time.Second)
	require.NotNil(t, s)
	assert.Len(t, s.pools, 2)
	require.NoError(t, s.Close())

	// 默认超时兜底。
	s = NewRedigoScanner([]string{"127.0.0.1:6390"}, "", 0)
	assert.NotNil(t, s)
	_ = s.Close()
}

func TestRedigoScanner_ScanWithPools(t *testing.T) {
	mkPool := func() *redis.Pool {
		return &redis.Pool{
			MaxIdle: 1,
			Dial: func() (redis.Conn, error) {
				return &fakeScanConn{t: t, cursors: []uint64{0}}, nil
			},
		}
	}
	s := &RedigoScanner{pools: []*redis.Pool{mkPool(), mkPool()}}
	next, keys, err := s.Scan(context.Background(), 0, "BATCH_TASK:*", 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), next)
	assert.Len(t, keys, 2, "两个实例的键集合并")

	// 拨号失败透传错误。
	bad := &RedigoScanner{pools: []*redis.Pool{{
		MaxIdle: 1,
		Dial: func() (redis.Conn, error) {
			return nil, errors.New("dial fail")
		},
	}}}
	_, _, err = bad.Scan(context.Background(), 0, "*", 10)
	require.Error(t, err)
}

func TestScanAllKeys(t *testing.T) {
	conn := &fakeScanConn{t: t, cursors: []uint64{17, 0}}
	keys, err := scanAllKeys(conn, "BATCH_TASK:*", 200)
	require.NoError(t, err)
	assert.Equal(t, 2, len(keys), "两轮 SCAN 各返回一批")
	assert.Equal(t, 2, conn.calls)

	// 游标立即归零：单轮。
	conn = &fakeScanConn{t: t, cursors: []uint64{0}}
	keys, err = scanAllKeys(conn, "BATCH_TASK:*", 200)
	require.NoError(t, err)
	assert.Equal(t, 1, len(keys))
	assert.Equal(t, 1, conn.calls)
}

func TestRedigoScanner_ScanPaging(t *testing.T) {
	scanner := &RedigoScanner{}
	// 无实例：cursor=0 返回空。
	next, keys, err := scanner.Scan(context.Background(), 0, "*", 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), next)
	assert.Empty(t, keys)

	// cursor!=0 恒返回空（全量已在首轮返回）。
	next, keys, err = scanner.Scan(context.Background(), 99, "*", 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), next)
	assert.Nil(t, keys)
}
