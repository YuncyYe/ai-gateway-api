// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dao

import (
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao/internal"
)

const tK8sPoolTableName = "k8s_pools"

// TK8sPool is the DAO representation of a k8s_pools row.
type TK8sPool struct {
	ID        int64     `db:"id"`
	Name      string    `db:"name"`
	Instances string    `db:"instances"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// TK8sPoolOne queries one k8s pool.
func TK8sPoolOne(dbCtx lib.DBContexter, where *TK8sPoolParam) (*TK8sPool, error) {
	t := &TK8sPool{}
	err := internal.QueryOne(dbCtx, tK8sPoolTableName, where, t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TK8sPoolList queries multiple k8s pools ordered by id.
func TK8sPoolList(dbCtx lib.DBContexter, where *TK8sPoolParam) ([]*TK8sPool, error) {
	if where == nil {
		where = &TK8sPoolParam{}
	}
	orderBy := "id"
	where.OrderBy = &orderBy
	t := []*TK8sPool{}
	err := internal.QueryList(dbCtx, tK8sPoolTableName, where, &t)
	if err == nil {
		return t, nil
	}
	if xerror.Cause(err) == internal.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

// TK8sPoolParam is used for create/update/delete/where conditions.
type TK8sPoolParam struct {
	ID        *int64     `db:"id"`
	Name      *string    `db:"name"`
	Instances *string    `db:"instances"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`

	OrderBy *string `db:"_orderby"`
}

// TK8sPoolCreate creates one or more k8s pool records.
func TK8sPoolCreate(dbCtx lib.DBContexter, data ...*TK8sPoolParam) (int64, error) {
	if len(data) == 1 {
		if data[0].CreatedAt == nil {
			data[0].CreatedAt = internal.PTimeNow()
		}
		if data[0].UpdatedAt == nil {
			data[0].UpdatedAt = data[0].CreatedAt
		}
		return internal.Create(dbCtx, tK8sPoolTableName, data[0])
	}

	list := make([]interface{}, len(data))
	for i, one := range data {
		if one.CreatedAt == nil {
			one.CreatedAt = internal.PTimeNow()
		}
		if one.UpdatedAt == nil {
			one.UpdatedAt = one.CreatedAt
		}
		list[i] = one
	}
	return internal.Create(dbCtx, tK8sPoolTableName, list...)
}

// TK8sPoolUpdate updates k8s pool records.
func TK8sPoolUpdate(dbCtx lib.DBContexter, val, where *TK8sPoolParam) (int64, error) {
	val.UpdatedAt = lib.PTimeNow()
	return internal.Update(dbCtx, tK8sPoolTableName, where, val)
}

// TK8sPoolDelete deletes k8s pool records matched by where.
func TK8sPoolDelete(dbCtx lib.DBContexter, where *TK8sPoolParam) (int64, error) {
	return internal.Delete(dbCtx, tK8sPoolTableName, where)
}
