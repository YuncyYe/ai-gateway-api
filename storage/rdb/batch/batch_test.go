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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/storage/rdb/internal/dao"
)

func TestBatchTaskDataToParam(t *testing.T) {
	terminalAt := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	task := &ibatch.BatchTask{
		BatchID:           "b-1",
		APIKeyID:          "ak-1",
		ProductName:       "p1",
		EntityID:          "e-1",
		Provider:          "openai",
		KeyName:           "k1",
		Endpoint:          "/v1/chat/completions",
		InputFileID:       "f-in",
		OutputFileID:      "f-out",
		Status:            "completed",
		RequestCounts:     map[string]int64{"completed": 3, "total": 4},
		EstLines:          5,
		UsageInputTokens:  10,
		UsageOutputTokens: 20,
		UsageSource:       "download",
		ReserveUnits:      100,
		SettleUnits:       90,
		SettleStatus:      "settled",
		OverReserved:      true,
		SyncSource:        "redis",
		IdemKey:           "b-1",
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
		TerminalAt:        &terminalAt,
	}

	param := batchTaskDataToParam(task)
	require.NotNil(t, param)
	require.NotNil(t, param.RequestCounts)
	assert.JSONEq(t, `{"completed":3,"total":4}`, *param.RequestCounts)
	assert.Equal(t, createdAt, *param.CreatedAt)
	assert.Equal(t, terminalAt, *param.TerminalAt)
	assert.True(t, *param.OverReserved)

	// 零值时间不写（DAO 填当前时间）。
	empty := batchTaskDataToParam(&ibatch.BatchTask{BatchID: "b-x", IdemKey: "b-x"})
	assert.Nil(t, empty.CreatedAt)
	assert.Nil(t, empty.UpdatedAt)
}

func TestBatchTaskParamToData(t *testing.T) {
	terminalAt := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	one := &dao.TBatchTask{
		ID:               7,
		BatchID:          "b-1",
		APIKeyID:         "ak-1",
		ProductName:      "p1",
		Provider:         "openai",
		Status:           "completed",
		RequestCounts:    `{"completed":3}`,
		EstLines:         5,
		UsageInputTokens: 10,
		SettleUnits:      90,
		SettleStatus:     "settled",
		OverReserved:     true,
		SyncSource:       "redis",
		IdemKey:          "b-1",
		TerminalAt:       &terminalAt,
	}

	task := batchTaskParamToData(one)
	assert.Equal(t, int64(7), task.ID)
	assert.Equal(t, int64(3), task.RequestCounts["completed"])
	assert.True(t, task.OverReserved)
	assert.Equal(t, terminalAt, *task.TerminalAt)

	// 空/坏 JSON 容忍。
	one.RequestCounts = "{bad"
	task = batchTaskParamToData(one)
	assert.Nil(t, task.RequestCounts)
}

func TestBatchTaskRoundTrip(t *testing.T) {
	original := &ibatch.BatchTask{
		BatchID: "b-rt", APIKeyID: "ak", ProductName: "p1", Provider: "openai",
		Status: "in_progress", SettleStatus: "reserved", IdemKey: "b-rt",
		RequestCounts: map[string]int64{"total": 9}, EstLines: 2,
		UsageInputTokens: 1, UsageOutputTokens: 2, ReserveUnits: 3, SettleUnits: 4,
		UsageSource: "download", SyncSource: "redis",
	}
	restored := batchTaskParamToData(daoRowOf(batchTaskDataToParam(original)))
	assert.Equal(t, original.RequestCounts, restored.RequestCounts)
	assert.Equal(t, original.EstLines, restored.EstLines)
	assert.Equal(t, original.SettleUnits, restored.SettleUnits)
	assert.Equal(t, original.UsageSource, restored.UsageSource)
	assert.Equal(t, original.SettleStatus, restored.SettleStatus)
}

// daoRowOf 把写入参数投影为查询行（测试用转换桥）。
func daoRowOf(p *dao.TBatchTaskParam) *dao.TBatchTask {
	row := &dao.TBatchTask{}
	if p.BatchID != nil {
		row.BatchID = *p.BatchID
	}
	if p.APIKeyID != nil {
		row.APIKeyID = *p.APIKeyID
	}
	if p.ProductName != nil {
		row.ProductName = *p.ProductName
	}
	if p.EntityID != nil {
		row.EntityID = *p.EntityID
	}
	if p.Provider != nil {
		row.Provider = *p.Provider
	}
	if p.KeyName != nil {
		row.KeyName = *p.KeyName
	}
	if p.Endpoint != nil {
		row.Endpoint = *p.Endpoint
	}
	if p.InputFileID != nil {
		row.InputFileID = *p.InputFileID
	}
	if p.OutputFileID != nil {
		row.OutputFileID = *p.OutputFileID
	}
	if p.Status != nil {
		row.Status = *p.Status
	}
	if p.RequestCounts != nil {
		row.RequestCounts = *p.RequestCounts
	}
	if p.EstLines != nil {
		row.EstLines = *p.EstLines
	}
	if p.UsageInputTokens != nil {
		row.UsageInputTokens = *p.UsageInputTokens
	}
	if p.UsageOutputTokens != nil {
		row.UsageOutputTokens = *p.UsageOutputTokens
	}
	if p.UsageSource != nil {
		row.UsageSource = *p.UsageSource
	}
	if p.ReserveUnits != nil {
		row.ReserveUnits = *p.ReserveUnits
	}
	if p.SettleUnits != nil {
		row.SettleUnits = *p.SettleUnits
	}
	if p.SettleStatus != nil {
		row.SettleStatus = *p.SettleStatus
	}
	if p.OverReserved != nil {
		row.OverReserved = *p.OverReserved
	}
	if p.SyncSource != nil {
		row.SyncSource = *p.SyncSource
	}
	if p.IdemKey != nil {
		row.IdemKey = *p.IdemKey
	}
	row.TerminalAt = p.TerminalAt
	return row
}

func TestBatchTaskFilterToParam(t *testing.T) {
	assert.Nil(t, batchTaskFilterToParam(nil))

	start := time.Now().Add(-time.Hour)
	end := time.Now()
	cursor := int64(99)
	filter := &ibatch.BatchTaskFilter{
		BatchID:      strp("b-1"),
		IdemKey:      strp("b-1"),
		ProductName:  strp("p1"),
		APIKeyID:     strp("ak"),
		EntityID:     strp("e-1"),
		Provider:     strp("openai"),
		Status:       strp("completed"),
		StatusIn:     []string{"expired", "failed"},
		StatusNotIn:  []string{"completed"},
		SettleStatus: strp("reserved"),
		CreatedStart: &start,
		CreatedEnd:   &end,
		CursorID:     &cursor,
	}
	param := batchTaskFilterToParam(filter)
	require.NotNil(t, param)
	assert.Equal(t, []string{"expired", "failed"}, param.StatusIn)
	assert.Equal(t, []string{"completed"}, param.StatusNotIn)
	assert.Equal(t, cursor, *param.IDGT)
	assert.Equal(t, start, *param.CreatedAtGTE)
	assert.Equal(t, end, *param.CreatedAtLTE)

	// 空切片不进入 where（gendry 对空切片报错）。
	empty := batchTaskFilterToParam(&ibatch.BatchTaskFilter{StatusIn: []string{}, StatusNotIn: []string{}})
	assert.Nil(t, empty.StatusIn)
	assert.Nil(t, empty.StatusNotIn)
}

func TestBatchFileConversions(t *testing.T) {
	file := &ibatch.BatchFile{
		FileID: "f-1", Provider: "openai", KeyName: "k1", APIKeyID: "ak",
		ProductName: "p1", Direction: "output", Purpose: "batch",
		Filename: "output.jsonl", Lines: 5, Bytes: 100,
	}
	param := batchFileDataToParam(file)
	require.NotNil(t, param)
	assert.Equal(t, "f-1", *param.FileID)
	assert.Equal(t, int64(5), *param.Lines)

	row := &dao.TBatchFile{
		ID: 3, FileID: "f-1", Provider: "openai", APIKeyID: "ak",
		ProductName: "p1", Direction: "output", Lines: 5, Bytes: 100,
		Filename: "output.jsonl",
	}
	restored := batchFileParamToData(row)
	assert.Equal(t, int64(3), restored.ID)
	assert.Equal(t, int64(100), restored.Bytes)
	// upsert 往返：参数层带 filename，读出后值不丢。
	assert.Equal(t, "output.jsonl", restored.Filename)
	require.NotNil(t, param.Filename)
	assert.Equal(t, "output.jsonl", *param.Filename)

	assert.Nil(t, batchFileFilterToParam(nil))
	fp := batchFileFilterToParam(&ibatch.BatchFileFilter{FileID: strp("f-1"), Provider: strp("openai")})
	require.NotNil(t, fp)
	assert.Equal(t, "f-1", *fp.FileID)
}

func strp(s string) *string { return &s }
