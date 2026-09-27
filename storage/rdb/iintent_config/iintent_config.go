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

package iintent_config

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iintent_config"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

// IntentConfigStorager is the RDB implementation of the intent config
// singleton storage.
type IntentConfigStorager struct {
	dbCtxFactory lib.DBContextFactory
}

func NewIntentConfigStorager(dbCtxFactory lib.DBContextFactory) *IntentConfigStorager {
	return &IntentConfigStorager{
		dbCtxFactory: dbCtxFactory,
	}
}

var _ iintent_config.IntentConfigStorager = &IntentConfigStorager{}

// Fetch reads the singleton row; returns nil, nil when unpublished.
func (s *IntentConfigStorager) Fetch(ctx context.Context) (*iintent_config.IntentConfig, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}

	one, err := dao.TIntentConfigOne(dbCtx, &dao.TIntentConfigParam{
		Id: lib.PInt64(iintent_config.IntentConfigFixedID),
	})
	if err != nil {
		return nil, err
	}
	if one == nil {
		return nil, nil
	}

	return intentConfigDataToModel(one), nil
}

// Upsert overwrites the singleton row (fixed id=1: insert when absent,
// overwrite when present). It must be called inside a transaction
// (itxn.TxnStorager).
func (s *IntentConfigStorager) Upsert(ctx context.Context, config *iintent_config.IntentConfig) error {
	if config == nil {
		return nil
	}

	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return err
	}

	return dao.TIntentConfigUpsert(dbCtx, intentConfigModelToData(config))
}

func intentConfigModelToData(config *iintent_config.IntentConfig) *dao.TIntentConfigParam {
	if config == nil {
		return nil
	}

	return &dao.TIntentConfigParam{
		Id:            lib.PInt64(config.Id),
		Version:       lib.PString(config.Version),
		MinConfidence: lib.PFloat64(config.MinConfidence),
		Questions:     lib.PString(config.Questions),
		CreatedAt:     &config.CreatedAt,
		UpdatedAt:     &config.UpdatedAt,
	}
}

func intentConfigDataToModel(one *dao.TIntentConfig) *iintent_config.IntentConfig {
	if one == nil {
		return nil
	}

	return &iintent_config.IntentConfig{
		Id:            one.Id,
		Version:       one.Version,
		MinConfidence: one.MinConfidence,
		Questions:     one.Questions,
		CreatedAt:     one.CreatedAt,
		UpdatedAt:     one.UpdatedAt,
	}
}
