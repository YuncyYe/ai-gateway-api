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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchFileToMeta(t *testing.T) {
	createdAt := time.Date(2026, 10, 6, 9, 55, 0, 0, time.UTC)
	f := &BatchFile{
		ID:          2001,
		FileID:      "file-1",
		Provider:    "deepseek",
		KeyName:     "key-1",
		APIKeyID:    "ak-1",
		ProductName: "p1",
		Direction:   "input",
		Purpose:     "batch_input",
		Lines:       10,
		Bytes:       4224,
		CreatedAt:   createdAt,
	}

	meta := f.ToMeta()
	assert.Equal(t, "file-1", meta.FileID)
	assert.Equal(t, "deepseek", meta.Provider)
	assert.Equal(t, "batch_input", meta.Purpose)
	assert.Equal(t, "", meta.Filename)
	assert.Equal(t, int64(4224), meta.Bytes)
	assert.Equal(t, createdAt, meta.CreatedAt)

	// 序列化后不得携带内部字段（id / key_name / api_key_id / product_name /
	// direction / lines / first_seen_at / last_seen_at / updated_at）。
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)
	s := string(raw)
	for _, field := range []string{
		`"id"`, `"key_name"`, `"api_key_id"`, `"product_name"`,
		`"direction"`, `"lines"`, `"first_seen_at"`, `"last_seen_at"`, `"updated_at"`,
	} {
		assert.NotContains(t, s, field)
	}
	assert.Contains(t, s, `"file_id":"file-1"`)

	assert.Nil(t, (*BatchFile)(nil).ToMeta())

	// issue #254：purpose 由方向映射（列内保留 provider 原值），filename 透出。
	t.Run("output 方向映射 batch_output，provider 原值不外泄", func(t *testing.T) {
		out := &BatchFile{FileID: "file-out", Provider: "openai", Direction: FileDirectionOutput, Purpose: "batch"}
		meta := out.ToMeta()
		assert.Equal(t, "batch_output", meta.Purpose)

		// direction 缺失（历史脏数据）时透传列值，不猜方向
		legacy := &BatchFile{FileID: "file-x", Provider: "openai", Purpose: "batch"}
		assert.Equal(t, "batch", legacy.ToMeta().Purpose)
	})

	t.Run("filename 透出且空值省略", func(t *testing.T) {
		withName := &BatchFile{FileID: "file-1", Provider: "openai", Direction: FileDirectionInput,
			Purpose: "batch", Filename: "input.jsonl"}
		assert.Equal(t, "input.jsonl", withName.ToMeta().Filename)

		raw, err := json.Marshal(withName.ToMeta())
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"filename":"input.jsonl"`)

		noName := &BatchFile{FileID: "file-2", Provider: "openai", Direction: FileDirectionOutput}
		raw, err = json.Marshal(noName.ToMeta())
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"filename"`, "空文件名省略该键")
	})
}

func TestBatchTaskBatchFilesOmitEmpty(t *testing.T) {
	// 无关联文件时 batch_files 省略（omitempty），列表行为不受影响。
	raw, err := json.Marshal(&BatchTask{BatchID: "b-1", Status: "queued"})
	assert.NoError(t, err)
	assert.NotContains(t, string(raw), "batch_files")

	raw, err = json.Marshal(&BatchTask{
		BatchID:    "b-1",
		Status:     "queued",
		BatchFiles: []*BatchFileMeta{{FileID: "f-1", Provider: "deepseek"}},
	})
	assert.NoError(t, err)
	assert.Contains(t, string(raw), `"batch_files":[{"file_id":"f-1","provider":"deepseek"`)
}
