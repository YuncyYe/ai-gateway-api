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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// scriptedRedis delegates GetInt64 to a func so tests can simulate real-Redis
// behaviours (nil replies, outages) on top of the in-memory mock.
type scriptedRedis struct {
	*stateful.MockRedisClient
	getInt64 func(key string) (int64, error)
}

func (s *scriptedRedis) GetInt64(key string) (int64, error) {
	return s.getInt64(key)
}

func TestNewCountCacheNilClientDisabled(t *testing.T) {
	assert.Nil(t, newCountCache(nil))
}

func TestCountCacheSetGetRoundTrip(t *testing.T) {
	c := newCountCache(stateful.NewMockRedisClient())
	filter := pagedFilter(1, 20, "admin")

	c.set(filter, 42)
	total, ok := c.get(filter)
	require.True(t, ok)
	assert.Equal(t, int64(42), total)

	// A legitimate total of 0 must be cached and served, not re-counted.
	other := pagedFilter(1, 20, "nobody")
	c.set(other, 0)
	total, ok = c.get(other)
	require.True(t, ok)
	assert.Equal(t, int64(0), total)
}

func TestCountCacheMissWhenEmpty(t *testing.T) {
	c := newCountCache(stateful.NewMockRedisClient())
	_, ok := c.get(pagedFilter(1, 20, "admin"))
	assert.False(t, ok)
}

func TestCountCacheInvalidateDropsAllFilters(t *testing.T) {
	c := newCountCache(stateful.NewMockRedisClient())
	c.set(pagedFilter(1, 20, "admin"), 73)
	c.set(pagedFilter(1, 20, "ops"), 5)

	c.invalidate()

	_, ok := c.get(pagedFilter(1, 20, "admin"))
	assert.False(t, ok)
	_, ok = c.get(pagedFilter(1, 20, "ops"))
	assert.False(t, ok)

	// New generation stores and serves normally.
	c.set(pagedFilter(1, 20, "admin"), 74)
	total, ok := c.get(pagedFilter(1, 20, "admin"))
	require.True(t, ok)
	assert.Equal(t, int64(74), total)
}

func TestCountCacheGenerationToleratesRedigoNil(t *testing.T) {
	mock := stateful.NewMockRedisClient()
	redis := &scriptedRedis{
		MockRedisClient: mock,
		getInt64: func(key string) (int64, error) {
			if key == countCacheGenKey {
				// Real redigo reports a missing key this way; the cache must
				// treat it as generation 0, not as an outage.
				return 0, errors.New("redigo: nil returned")
			}
			return mock.GetInt64(key)
		},
	}
	c := newCountCache(redis)

	c.set(pagedFilter(1, 20, "admin"), 9)
	total, ok := c.get(pagedFilter(1, 20, "admin"))
	require.True(t, ok)
	assert.Equal(t, int64(9), total)
}

func TestCountCacheRedisFailureDegradesToMiss(t *testing.T) {
	redis := &scriptedRedis{
		MockRedisClient: stateful.NewMockRedisClient(),
		getInt64: func(key string) (int64, error) {
			return 0, errors.New("connection refused")
		},
	}
	c := newCountCache(redis)

	_, ok := c.get(pagedFilter(1, 20, "admin"))
	assert.False(t, ok)
	// set must not panic or propagate.
	c.set(pagedFilter(1, 20, "admin"), 9)
}

func TestCountCacheFilterHashStableAndDistinct(t *testing.T) {
	base := pagedFilter(3, 20, "admin")

	assert.Equal(t, countCacheFilterHash(base), countCacheFilterHash(pagedFilter(3, 20, "admin")))

	// Page and page size must not change the key: the total is per filter.
	assert.Equal(t, countCacheFilterHash(base), countCacheFilterHash(pagedFilter(9, 100, "admin")))

	// A nil filter equals an empty filter (unfiltered count).
	assert.Equal(t, countCacheFilterHash(nil), countCacheFilterHash(&ioperlog.OperationLogFilter{}))

	variations := map[string]*ioperlog.OperationLogFilter{
		"log_id":            {LogID: strPtr("log-1")},
		"operator_type":     {OperatorType: operatorTypePtr(ioperlog.OperatorTypeToken)},
		"operator_id":       {OperatorID: int64Ptr(7)},
		"action":            {Action: strPtr("update")},
		"resource_type":     {ResourceType: strPtr("entity")},
		"resource_id":       {ResourceID: strPtr("entity-5")},
		"resource_name":     {ResourceName: strPtr("rd-team")},
		"resource_parent":   {ResourceParentID: strPtr("entity-1")},
		"status":            {Status: int8Ptr(2)},
		"start_time":        {StartTime: timePtr(time.Unix(1725091200, 0))},
		"end_time":          {EndTime: timePtr(time.Unix(1725091200, 0))},
		"operator_name":     {OperatorName: strPtr("ops")},
	}
	baseHash := countCacheFilterHash(&ioperlog.OperationLogFilter{})
	for name, f := range variations {
		assert.NotEqual(t, baseHash, countCacheFilterHash(f), "field %s must change the key", name)
	}

	// Separator injection: a value embedding the field grammar must not alias
	// a structured filter ("x|status=2" as a name != name "x" + status 2).
	injected := &ioperlog.OperationLogFilter{OperatorName: strPtr("x|status=2")}
	structured := &ioperlog.OperationLogFilter{OperatorName: strPtr("x"), Status: int8Ptr(2)}
	assert.NotEqual(t, countCacheFilterHash(injected), countCacheFilterHash(structured))
}

func strPtr(s string) *string { return &s }

func int64Ptr(v int64) *int64 { return &v }

func int8Ptr(v int8) *int8 { return &v }

func operatorTypePtr(v ioperlog.OperatorType) *ioperlog.OperatorType { return &v }

func timePtr(v time.Time) *time.Time { return &v }
