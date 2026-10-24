// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package quota

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/quota"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

func TestQuotaPlanFilterToParam(t *testing.T) {
	id := int64(1)

	param := quotaPlanFilterToParam(&quota.QuotaPlanFilter{
		ID: &id,
	})

	assert.Equal(t, &id, param.ID)
	assert.Nil(t, quotaPlanFilterToParam(nil))
}

func TestQuotaPlanDataToParam(t *testing.T) {
	unlimited := true
	passWhenNoEnoughQuota := false
	quotaValue := float64(123.45678901)
	unit := "RMB"
	resetPeriod := "monthly"

	param := quotaPlanDataToParam(&quota.QuotaPlanParam{
		Unlimited:             &unlimited,
		PassWhenNoEnoughQuota: &passWhenNoEnoughQuota,
		Quota:                 &quotaValue,
		Unit:                  &unit,
		ResetPeriod:           &resetPeriod,
	})

	assert.Equal(t, &unlimited, param.Unlimited)
	assert.Equal(t, &passWhenNoEnoughQuota, param.PassWhenNoEnoughQuota)
	assert.NotNil(t, param.Quota)
	assert.True(t, decimal.NewFromFloat(quotaValue).Equal(*param.Quota))
	assert.Equal(t, &unit, param.Unit)
	assert.Equal(t, &resetPeriod, param.ResetPeriod)
}

func TestQuotaPlanParamToData(t *testing.T) {
	data := &dao.TQuotaPlan{
		ID:                    1,
		Unlimited:             true,
		PassWhenNoEnoughQuota: false,
		Quota:                 decimal.NewFromFloat(999.99),
		Unit:                  "RMB",
		ResetPeriod:           "weekly",
	}

	param := quotaPlanParamToData(data)

	assert.Equal(t, int64(1), *param.ID)
	assert.True(t, *param.Unlimited)
	assert.False(t, *param.PassWhenNoEnoughQuota)
	assert.Equal(t, float64(999.99), *param.Quota)
	assert.Equal(t, "RMB", *param.Unit)
	assert.Equal(t, "weekly", *param.ResetPeriod)
	assert.NotNil(t, param.CreateTime)
}

func TestFloat64PtrToDecimalPtr(t *testing.T) {
	assert.Nil(t, float64PtrToDecimalPtr(nil))

	v := float64(0.00000001)
	d := float64PtrToDecimalPtr(&v)
	assert.NotNil(t, d)
	assert.True(t, decimal.NewFromFloat(v).Equal(*d))
}

func TestDecimalToFloat64Ptr(t *testing.T) {
	assert.Equal(t, float64(0), *decimalToFloat64Ptr(decimal.Zero))
	assert.Equal(t, float64(1.5), *decimalToFloat64Ptr(decimal.NewFromFloat(1.5)))
}

func TestPFloat64(t *testing.T) {
	p := lib.PFloat64(3.14)
	assert.Equal(t, float64(3.14), *p)
}

func TestInitLastResetAtAtCreate(t *testing.T) {
	weekly := "weekly"
	monthly := "monthly"
	never := "never"

	t.Run("weekly initializes last_reset_at", func(t *testing.T) {
		data := &dao.TQuotaPlanParam{ResetPeriod: &weekly}
		initLastResetAtAtCreate(data)
		assert.NotNil(t, data.LastResetAt)
	})

	t.Run("monthly initializes last_reset_at", func(t *testing.T) {
		data := &dao.TQuotaPlanParam{ResetPeriod: &monthly}
		initLastResetAtAtCreate(data)
		assert.NotNil(t, data.LastResetAt)
	})

	t.Run("never keeps nil last_reset_at", func(t *testing.T) {
		data := &dao.TQuotaPlanParam{ResetPeriod: &never}
		initLastResetAtAtCreate(data)
		assert.Nil(t, data.LastResetAt)
	})

	t.Run("nil reset_period keeps nil last_reset_at", func(t *testing.T) {
		data := &dao.TQuotaPlanParam{}
		initLastResetAtAtCreate(data)
		assert.Nil(t, data.LastResetAt)
	})

	t.Run("existing last_reset_at is preserved", func(t *testing.T) {
		existing := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		data := &dao.TQuotaPlanParam{ResetPeriod: &monthly, LastResetAt: &existing}
		initLastResetAtAtCreate(data)
		assert.Equal(t, existing, *data.LastResetAt)
	})
}
