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

package batch

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

// UpsertBatchFile 按 (file_id, provider) 幂等 upsert。
func (s *BatchStorager) UpsertBatchFile(ctx context.Context, file *ibatch.BatchFile) (int64, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return 0, err
	}
	return dao.TBatchFileUpsertByFileProvider(dbCtx, batchFileDataToParam(file))
}

// FetchBatchFile 查询单条文件元数据（file_id + provider 唯一定位）。
func (s *BatchStorager) FetchBatchFile(ctx context.Context, filter *ibatch.BatchFileFilter) (*ibatch.BatchFile, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}
	one, err := dao.TBatchFileOne(dbCtx, batchFileFilterToParam(filter))
	if err != nil {
		return nil, err
	}
	if one == nil {
		return nil, nil
	}
	return batchFileParamToData(one), nil
}

// ListBatchFiles 查询文件列表（id 升序）。
func (s *BatchStorager) ListBatchFiles(ctx context.Context, filter *ibatch.BatchFileFilter) ([]*ibatch.BatchFile, error) {
	dbCtx, err := s.dbCtxFactory(ctx)
	if err != nil {
		return nil, err
	}
	where := batchFileFilterToParam(filter)
	where.OrderBy = lib.PString("id")
	list, err := dao.TBatchFileList(dbCtx, where)
	if err != nil {
		return nil, err
	}
	rst := make([]*ibatch.BatchFile, 0, len(list))
	for _, one := range list {
		rst = append(rst, batchFileParamToData(one))
	}
	return rst, nil
}

func batchFileFilterToParam(filter *ibatch.BatchFileFilter) *dao.TBatchFileParam {
	if filter == nil {
		return nil
	}
	return &dao.TBatchFileParam{
		ID:          filter.ID,
		FileID:      filter.FileID,
		Provider:    filter.Provider,
		APIKeyID:    filter.APIKeyID,
		ProductName: filter.ProductName,
		Direction:   filter.Direction,
	}
}

func batchFileDataToParam(file *ibatch.BatchFile) *dao.TBatchFileParam {
	data := &dao.TBatchFileParam{
		FileID:      lib.PString(file.FileID),
		Provider:    lib.PString(file.Provider),
		KeyName:     lib.PString(file.KeyName),
		APIKeyID:    lib.PString(file.APIKeyID),
		ProductName: lib.PString(file.ProductName),
		Direction:   lib.PString(file.Direction),
		Purpose:     lib.PString(file.Purpose),
		Filename:    lib.PString(file.Filename),
		Lines:       lib.PInt64(file.Lines),
		Bytes:       lib.PInt64(file.Bytes),
		FirstSeenAt: file.FirstSeenAt,
		LastSeenAt:  file.LastSeenAt,
	}
	if !file.CreatedAt.IsZero() {
		data.CreatedAt = lib.PTime(file.CreatedAt)
	}
	if !file.UpdatedAt.IsZero() {
		data.UpdatedAt = lib.PTime(file.UpdatedAt)
	}
	return data
}

func batchFileParamToData(one *dao.TBatchFile) *ibatch.BatchFile {
	return &ibatch.BatchFile{
		ID:          one.ID,
		FileID:      one.FileID,
		Provider:    one.Provider,
		KeyName:     one.KeyName,
		APIKeyID:    one.APIKeyID,
		ProductName: one.ProductName,
		Direction:   one.Direction,
		Purpose:     one.Purpose,
		Filename:    one.Filename,
		Lines:       one.Lines,
		Bytes:       one.Bytes,
		FirstSeenAt: one.FirstSeenAt,
		LastSeenAt:  one.LastSeenAt,
		CreatedAt:   one.CreatedAt,
		UpdatedAt:   one.UpdatedAt,
	}
}
