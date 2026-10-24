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
	"fmt"
	"time"

	"github.com/bfenetworks/bfe/bfe_util/redis_client"
	"github.com/gomodule/redigo/redis"
)

// RedisClientAdapter 将 bfe redis_client.Client 适配为 ibatch.RedisClient：
//
//   - HGetAll/HSetWithTTL/SetNXWithTTL/ReserveDecr 走单 key Lua（与数据面
//     mod_ai_batch / mod_ai_token_auth 的脚本同语义，Redis Cluster 安全），
//     对既有使用方零影响——底层客户端接口未做任何扩展；
//   - Scan 由注入的 RedisScanner 提供（生产 RedigoScanner 遍历全部后端
//     实例；底层客户端不支持 SCAN，且 BFE 客户端按 key 一致性哈希分片，
//     单实例 SCAN 会漏键，故必须显式遍历实例，不能用 KEYS 兜底）。
type RedisClientAdapter struct {
	client  redis_client.Client
	scanner RedisScanner
}

// RedisScanner 提供 SCAN 能力。生产实现为 RedigoScanner。
type RedisScanner interface {
	// Scan 语义：cursor=0 时返回 match 的全部键（内部完成各实例游标
	// 迭代）并返回 nextCursor=0；cursor!=0 时返回 (0, nil, nil)。
	// 调用方应只以 cursor=0 发起、消费返回的全部 keys。
	Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error)
}

// NewRedisClientAdapter 创建适配器。client 为 nil 时全部操作返回错误
// （装配保证非 nil）；scanner 为 nil 时 Scan 返回空（同步职责降级跳过）。
func NewRedisClientAdapter(client redis_client.Client, scanner RedisScanner) *RedisClientAdapter {
	return &RedisClientAdapter{client: client, scanner: scanner}
}

// Lua 脚本源（与数据面同语义，见 bfe mod_ai_batch/batch_state.go、
// mod_ai_token_auth/batch_quota.go）。
const (
	luaAdapterHashGetAll = `
return redis.call('HGETALL', KEYS[1])
`
	luaAdapterHashSetWithTTL = `
local n = redis.call('HSET', KEYS[1], unpack(ARGV, 2))
redis.call('EXPIRE', KEYS[1], ARGV[1])
return n
`
	luaAdapterSetNXWithTTL = `
return redis.call('SET', KEYS[1], '1', 'NX', 'EX', ARGV[1])
`
	luaAdapterReserveDecr = `
local n = redis.call('GET', KEYS[1])
if n == false then return 0 end
local cur = tonumber(n)
local d = math.min(cur, tonumber(ARGV[1]))
if d > 0 then redis.call('DECRBY', KEYS[1], d) end
return d
`
)

func (a *RedisClientAdapter) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	if a.client == nil {
		return nil, fmt.Errorf("ibatch: redis client is nil")
	}
	reply, err := a.client.NewScript(luaAdapterHashGetAll).Run(key)
	if err != nil {
		if redis_client.IsKeyNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	vals, err := redis.StringMap(reply, nil)
	if err != nil || len(vals) == 0 {
		return nil, nil // 键不存在或为空 hash 均按 miss 处理
	}
	return vals, nil
}

func (a *RedisClientAdapter) HSetWithTTL(ctx context.Context, key string, ttlSeconds int, fields map[string]string) error {
	if a.client == nil {
		return fmt.Errorf("ibatch: redis client is nil")
	}
	args := make([]interface{}, 0, 1+len(fields)*2)
	args = append(args, ttlSeconds)
	for k, v := range fields {
		args = append(args, k, v)
	}
	_, err := a.client.NewScript(luaAdapterHashSetWithTTL).Run(key, args...)
	return err
}

func (a *RedisClientAdapter) SetNXWithTTL(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	if a.client == nil {
		return false, fmt.Errorf("ibatch: redis client is nil")
	}
	reply, err := a.client.NewScript(luaAdapterSetNXWithTTL).Run(key, ttlSeconds)
	if err != nil {
		return false, err
	}
	s, _ := redis.String(reply, nil)
	return s == "OK", nil
}

func (a *RedisClientAdapter) ReserveDecr(ctx context.Context, key string, delta int64) (int64, error) {
	if a.client == nil {
		return 0, fmt.Errorf("ibatch: redis client is nil")
	}
	reply, err := a.client.NewScript(luaAdapterReserveDecr).Run(key, delta)
	if err != nil {
		return 0, err
	}
	decr, err := redis.Int64(reply, nil)
	if err != nil {
		return 0, nil // 键不存在视为无可扣减（数据面同语义返回 0）
	}
	return decr, nil
}

func (a *RedisClientAdapter) Delete(ctx context.Context, key string) error {
	if a.client == nil {
		return fmt.Errorf("ibatch: redis client is nil")
	}
	return a.client.Delete(key)
}

func (a *RedisClientAdapter) Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
	if a.scanner == nil {
		return 0, nil, nil
	}
	return a.scanner.Scan(ctx, cursor, match, count)
}

// RedigoScanner 基于 redigo 直连全部 Redis 后端实例的 SCAN 实现。
// BFE redis 客户端按 key 哈希分片到多实例，逐实例 SCAN 后合并结果。
type RedigoScanner struct {
	pools []*redis.Pool
}

// NewRedigoScanner 为每个 addr（host:port）建一个连接池。
// timeout 为单次连接/读/写超时。
func NewRedigoScanner(addrs []string, password string, timeout time.Duration) *RedigoScanner {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	s := &RedigoScanner{}
	for _, addr := range addrs {
		addr := addr
		pool := &redis.Pool{
			MaxIdle:   2,
			MaxActive: 4,
			Wait:      true,
			Dial: func() (redis.Conn, error) {
				conn, err := redis.DialTimeout("tcp", addr, timeout, timeout, timeout)
				if err != nil {
					return nil, err
				}
				if password != "" {
					if _, err := conn.Do("AUTH", password); err != nil {
						conn.Close()
						return nil, err
					}
				}
				return conn, nil
			},
		}
		s.pools = append(s.pools, pool)
	}
	return s
}

// Close 释放全部连接池（进程生命周期内可不调用，测试用）。
func (s *RedigoScanner) Close() error {
	for _, pool := range s.pools {
		pool.Close()
	}
	return nil
}

// Scan 首轮（cursor=0）遍历全部实例返回完整键集合；后续调用返回空。
func (s *RedigoScanner) Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
	if cursor != 0 {
		return 0, nil, nil
	}
	if count <= 0 {
		count = 200
	}
	var all []string
	for _, pool := range s.pools {
		conn, err := pool.GetContext(ctx)
		if err != nil {
			return 0, nil, fmt.Errorf("ibatch: scanner get conn: %v", err)
		}
		keys, err := scanAllKeys(conn, match, int(count))
		conn.Close()
		if err != nil {
			return 0, nil, fmt.Errorf("ibatch: scanner scan: %v", err)
		}
		all = append(all, keys...)
	}
	return 0, all, nil
}

// scanAllKeys 在单连接上游标 SCAN 至结束（游标回归 0）。
// redigo v2 未提供 SCAN 游标助手，直接组装 SCAN/MATCH/COUNT 命令。
func scanAllKeys(conn redis.Conn, match string, count int) ([]string, error) {
	var keys []string
	cursor := uint64(0)
	for {
		values, err := redis.Values(conn.Do("SCAN", cursor, "MATCH", match, "COUNT", count))
		if err != nil {
			return nil, err
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("ibatch: unexpected SCAN reply: %d elements", len(values))
		}
		next, err := redis.Uint64(values[0], nil)
		if err != nil {
			return nil, err
		}
		batch, err := redis.Strings(values[1], nil)
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		if next == 0 {
			return keys, nil
		}
		cursor = next
	}
}
