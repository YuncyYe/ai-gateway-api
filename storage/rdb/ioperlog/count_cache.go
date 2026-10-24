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
//limitations under the License.

package ioperlog

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"

	"github.com/bfenetworks/bfe/bfe_util/redis_client"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
)

const (
	// countCacheGenKey is the monotonic generation counter. Every successful
	// BatchCreate bumps it, so totals cached under older generations become
	// unreachable and expire out by TTL.
	countCacheGenKey = "OPERLOG_COUNT_GEN"
	// countCacheTTL bounds staleness to one async flush cycle (5 s) plus
	// margin, and only matters for flushes from other instances sharing the
	// Redis: locally, invalidation is exact.
	countCacheTTL = 10
)

// countCache serves exact pagination totals per filter from Redis, so paging
// through one filtered result set does not repeat the count query on every
// page (issue #280). It stores exact totals, never approximations: a cached
// value is the count of a state no older than the last invalidation, and
// buffered-not-yet-flushed entries are invisible to a live count anyway
// (writes are asynchronous batches). Any Redis failure degrades to a miss,
// i.e. the caller runs the live count.
type countCache struct {
	client redis_client.Client
}

// newCountCache returns nil (cache disabled) when client is nil.
func newCountCache(client redis_client.Client) *countCache {
	if client == nil {
		return nil
	}
	return &countCache{client: client}
}

// get returns the cached exact total for the filter under the current
// generation; ok is false on any miss or Redis failure.
func (c *countCache) get(filter *ioperlog.OperationLogFilter) (int64, bool) {
	gen, ok := c.generation()
	if !ok {
		return 0, false
	}
	raw, err := c.client.GetInt64(countCacheValueKey(gen, filter))
	if err != nil || raw <= 0 {
		// err also covers real-Redis misses ("redigo: nil returned"); raw == 0
		// is the reserved miss sentinel (totals are stored offset by +1, so a
		// legitimate total of 0 is cached as 1 and served correctly).
		return 0, false
	}
	return raw - 1, true
}

// set stores the exact total for the filter under the current generation.
// Best effort: failures are ignored so the cache never blocks the request.
func (c *countCache) set(filter *ioperlog.OperationLogFilter, total int64) {
	gen, ok := c.generation()
	if !ok {
		return
	}
	_ = c.client.Setex(countCacheValueKey(gen, filter),
		[]byte(strconv.FormatInt(total+1, 10)), countCacheTTL)
}

// invalidate bumps the generation counter after a successful flush.
func (c *countCache) invalidate() {
	_, _ = c.client.Incr(countCacheGenKey)
}

// generation reads the current generation. A fresh Redis reports the missing
// counter as "redigo: nil returned"; that is generation 0, not a failure.
func (c *countCache) generation() (int64, bool) {
	gen, err := c.client.GetInt64(countCacheGenKey)
	if err != nil {
		if strings.Contains(err.Error(), "redigo: nil returned") {
			return 0, true
		}
		return 0, false
	}
	return gen, true
}

// countCacheValueKey namespaces the total by generation: old-generation
// entries are never read again and expire by TTL without deletion.
func countCacheValueKey(gen int64, filter *ioperlog.OperationLogFilter) string {
	return "OPERLOG_COUNT:G" + strconv.FormatInt(gen, 10) + ":" + countCacheFilterHash(filter)
}

// countCacheFilterHash serializes the filter deterministically (Page/PageSize
// excluded — the total is identical across pages) and hashes it. Values are
// QueryEscape'd so field separators cannot collide with value content.
func countCacheFilterHash(filter *ioperlog.OperationLogFilter) string {
	if filter == nil {
		filter = &ioperlog.OperationLogFilter{}
	}
	var b strings.Builder
	b.WriteString("v1")
	writeOptString(&b, "log_id", filter.LogID)
	if filter.OperatorType != nil {
		b.WriteString("|operator_type=" + strconv.FormatInt(int64(*filter.OperatorType), 10))
	}
	if filter.OperatorID != nil {
		b.WriteString("|operator_id=" + strconv.FormatInt(*filter.OperatorID, 10))
	}
	writeOptString(&b, "operator_name", filter.OperatorName)
	writeOptString(&b, "action", filter.Action)
	writeOptString(&b, "resource_type", filter.ResourceType)
	writeOptString(&b, "resource_id", filter.ResourceID)
	writeOptString(&b, "resource_name", filter.ResourceName)
	writeOptString(&b, "resource_parent_id", filter.ResourceParentID)
	if filter.Status != nil {
		b.WriteString("|status=" + strconv.FormatInt(int64(*filter.Status), 10))
	}
	if filter.StartTime != nil {
		b.WriteString("|start_time=" + strconv.FormatInt(filter.StartTime.Unix(), 10))
	}
	if filter.EndTime != nil {
		b.WriteString("|end_time=" + strconv.FormatInt(filter.EndTime.Unix(), 10))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func writeOptString(b *strings.Builder, name string, v *string) {
	b.WriteString("|" + name + "=")
	if v != nil {
		b.WriteString(url.QueryEscape(*v))
	}
}
