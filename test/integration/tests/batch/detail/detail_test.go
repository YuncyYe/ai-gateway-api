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

package detail_test

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	helper "github.com/rainway-ai-gateway/ai-gateway-api/integration/tests/batch/helper"
	"github.com/rainway-ai-gateway/ai-gateway-api/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sm *testutil.ServerManager

func TestMain(m *testing.M) {
	var err error
	sm, err = testutil.StartServerAuto()
	if err != nil {
		panic("failed to start server: " + err.Error())
	}
	code := m.Run()
	sm.Shutdown()
	os.Exit(code)
}

// ---------- BT-2-000 可达性冒烟：产品线回退后可进入业务逻辑（ghost → 404） ----------

func TestBatchDetail_Reachable(t *testing.T) {
	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + helper.Unique("ghost"))
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-2-001 任务不存在 → 404 BATCH_NOT_FOUND ----------

func TestBatchDetail_NotFound(t *testing.T) {
	helper.RequireBatchAPI(t)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + helper.Unique("ghost"))
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-2-002 直写一行 → 200 且字段完整（合同形状锁定，issue #249） ----------

// TestBatchDetail_FullFields 按 batches.md §3.2 合同断言：Data 顶层即任务字段
//（无 task/files 包装层），关联文件在 batch_files（§2.2 形状）。
// 反面模式 #5 警示：本用例曾按实现现状断言 task/files 包装与内部 direction
// 字段，把缺陷固化成通行证；修复后以合同为唯一断言依据。
func TestBatchDetail_FullFields(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-002")
	row := helper.BatchTaskRow{
		BatchID:      tag,
		APIKeyID:     tag + "-ak",
		ProductName:  helper.TestProductName,
		EntityID:     tag + "-entity",
		Provider:     "deepseek",
		KeyName:      "key-primary",
		Endpoint:     "/v1/batches",
		InputFileID:  tag + "-in",
		OutputFileID: tag + "-out",
		Status:       "in_progress",
		SettleStatus: "reserved",
		ReserveUnits: 250000000, // 2.5 RMB，1e-8 定点整数
		OverReserved: 1,
		SyncSource:   "log",
		IdemKey:      tag + "-idem",
	}
	helper.InsertBatchTask(t, db, row)
	defer helper.DeleteBatchTask(t, db, tag)

	helper.InsertBatchFile(t, db, helper.BatchFileRow{
		FileID: tag + "-in", Provider: "deepseek", APIKeyID: tag + "-ak",
		ProductName: helper.TestProductName, Direction: "input", Purpose: "batch_input",
		Filename: "input.jsonl", Lines: 10, Bytes: 1024,
	})
	helper.InsertBatchFile(t, db, helper.BatchFileRow{
		FileID: tag + "-out", Provider: "deepseek", APIKeyID: tag + "-ak",
		ProductName: helper.TestProductName, Direction: "output", Purpose: "batch_output",
		Filename: "output.jsonl", Lines: 9, Bytes: 2048,
	})
	defer helper.DeleteBatchFile(t, db, tag+"-in", "deepseek")
	defer helper.DeleteBatchFile(t, db, tag+"-out", "deepseek")

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + tag)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))

	// 顶层形状锁（家族11：防包装层回归——Data 必须是任务对象本身）。
	_, hasTask := data["task"]
	_, hasFiles := data["files"]
	assert.False(t, hasTask, "Data.task wrapper must not exist (batches.md §3.2)")
	assert.False(t, hasFiles, "Data.files wrapper must not exist (batches.md §3.2)")

	// 任务字段在 Data 顶层（§2.1）。
	assert.Equal(t, tag, data["batch_id"])
	assert.Equal(t, tag+"-ak", data["api_key_id"])
	assert.Equal(t, helper.TestProductName, data["product_name"])
	assert.Equal(t, tag+"-entity", data["entity_id"])
	assert.Equal(t, "deepseek", data["provider"])
	assert.Equal(t, "/v1/batches", data["endpoint"])
	assert.Equal(t, tag+"-in", data["input_file_id"])
	assert.Equal(t, tag+"-out", data["output_file_id"])
	assert.Equal(t, "in_progress", data["status"])
	assert.Equal(t, "reserved", data["settle_status"])
	assert.Equal(t, true, data["over_reserved"])
	assert.Equal(t, "log", data["sync_source"])

	// reserve_units 必须按 1e-8 定点整数输出：值为 250000000 且
	// 原始报文 token 无小数点/科学计数法（不做 ÷1e8 换算）。
	assert.Equal(t, float64(250000000), data["reserve_units"])
	needle := strconv.Quote("reserve_units") + ":"
	idx := strings.LastIndex(string(resp.RawBody), needle)
	require.NotEqual(t, -1, idx)
	rest := string(resp.RawBody)[idx+len(needle):]
	end := strings.IndexAny(rest, ",}")
	require.NotEqual(t, -1, end)
	token := strings.TrimSpace(rest[:end])
	assert.Equal(t, "250000000", token, "reserve_units must serialize as integer token")

	// 关联 batch_files（输入/输出）按 §2.2 形状组装在 Data 顶层。
	filesVal, ok := data["batch_files"]
	require.True(t, ok, "Data.batch_files must exist")
	files, ok := filesVal.([]interface{})
	require.True(t, ok, "Data.batch_files must be array")
	require.Equal(t, 2, len(files))
	byID := map[string]map[string]interface{}{}
	for _, item := range files {
		f, ok := item.(map[string]interface{})
		require.True(t, ok, "batch_files element must be object")
		// 键集合精确锁：恰为 §2.2 六字段（issue #254 起 filename 由 provider
		// File 对象落库并输出），不得泄漏内部字段
		//（id/direction/lines/key_name/api_key_id/product_name/first_seen_at/...）。
		keys := make([]string, 0, len(f))
		for k := range f {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t,
			[]string{"file_id", "provider", "purpose", "filename", "bytes", "created_at"}, keys,
			"batch_files element keys must exactly match contract (batches.md §2.2)")
		byID[f["file_id"].(string)] = f
	}
	require.Contains(t, byID, tag+"-in")
	require.Contains(t, byID, tag+"-out")
	assert.Equal(t, "batch_input", byID[tag+"-in"]["purpose"])
	assert.Equal(t, "batch_output", byID[tag+"-out"]["purpose"])
	// issue #254：filename 来自 provider File 对象（数据面同步落库）
	assert.Equal(t, "input.jsonl", byID[tag+"-in"]["filename"])
	assert.Equal(t, "output.jsonl", byID[tag+"-out"]["filename"])
	assert.Equal(t, float64(1024), byID[tag+"-in"]["bytes"])
	assert.Equal(t, float64(2048), byID[tag+"-out"]["bytes"])

	// 原始报文不得出现文件内部字段（direction 为一期直出缺陷的典型泄漏键）。
	assert.NotContains(t, string(resp.RawBody), `"direction"`)
	assert.NotContains(t, string(resp.RawBody), `"first_seen_at"`)
}

// ---------- BT-2-005 列内 purpose 为 provider 原值 → 视图按方向映射契约枚举（issue #254） ----------

// TestBatchDetail_PurposeMappedFromDirection 锁定"batch_files.purpose 列保留
// provider 原值（batch），对外视图按 direction 映射为 batch_input/batch_output"
// 的语义（batches.md §2.2）；同时验证 filename 缺失时该键省略。
func TestBatchDetail_PurposeMappedFromDirection(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-005")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: "openai", Status: "in_progress", SettleStatus: "reserved",
		InputFileID: tag + "-in", OutputFileID: tag + "-out",
		SyncSource: "redis", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	// 数据面原样写入 provider 的 purpose=batch；filename 缺失
	helper.InsertBatchFile(t, db, helper.BatchFileRow{
		FileID: tag + "-in", Provider: "openai", APIKeyID: tag + "-ak",
		ProductName: helper.TestProductName, Direction: "input", Purpose: "batch", Bytes: 206,
	})
	helper.InsertBatchFile(t, db, helper.BatchFileRow{
		FileID: tag + "-out", Provider: "openai", APIKeyID: tag + "-ak",
		ProductName: helper.TestProductName, Direction: "output", Purpose: "batch", Bytes: 512,
	})
	defer helper.DeleteBatchFile(t, db, tag+"-in", "openai")
	defer helper.DeleteBatchFile(t, db, tag+"-out", "openai")

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + tag)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	filesVal, ok := data["batch_files"]
	require.True(t, ok)
	files, ok := filesVal.([]interface{})
	require.True(t, ok)
	require.Equal(t, 2, len(files))

	byID := map[string]map[string]interface{}{}
	for _, item := range files {
		f, ok := item.(map[string]interface{})
		require.True(t, ok)
		byID[f["file_id"].(string)] = f
	}
	assert.Equal(t, "batch_input", byID[tag+"-in"]["purpose"])
	assert.Equal(t, "batch_output", byID[tag+"-out"]["purpose"])
	_, hasFilename := byID[tag+"-in"]["filename"]
	assert.False(t, hasFilename, "filename 缺失时该键须省略")
	// 原始报文不得出现 provider 原值 purpose（batch）以外的 internal 泄漏
	assert.NotContains(t, string(resp.RawBody), `"direction"`)
}

// ---------- BT-2-004 无关联文件 → 200 且 batch_files 键缺席（omitempty） ----------

func TestBatchDetail_NoFiles(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-004")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: "deepseek", Status: "queued", SettleStatus: "reserved",
		SyncSource: "redis", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + tag)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.Equal(t, tag, data["batch_id"])
	_, hasFiles := data["batch_files"]
	assert.False(t, hasFiles, "batch_files must be omitted when no associated files (omitempty)")
}

// ---------- BT-2-003 产品线隔离：其它产品线的任务按不存在处理 ----------

func TestBatchDetail_ProductIsolation(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-003")
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: "OTHER_product",
		Provider: "deepseek", Status: "in_progress", IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + tag)
	require.NoError(t, err)
	testutil.AssertErrCode(t, resp, 404)
}

// ---------- BT-2-006 零值终态任务：§2.1 必现键显式输出 0/""（issue #271） ----------

// TestBatchDetail_ZeroValuesExplicit 锁定 batches.md §2.1"字段必现规则"
//（issue #271）：int64 零值与空串必须显式出现，不得被 omitempty 吞掉。
// settled 与 released 两种终态各一行（issue 复现面）；种子经 helper
// 直写（est_lines/usage/settle_units=0、usage_source=''、terminal_at=NULL），
// 与 issue 的 Redis 回读数据同形。当前 HEAD 上本用例必须失败（复现
// issue），修复后通过。
func TestBatchDetail_ZeroValuesExplicit(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-006")
	rows := []helper.BatchTaskRow{
		{BatchID: tag + "-settled", APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
			Provider: "deepseek", Status: "completed", SettleStatus: "settled",
			ReserveUnits: 0, SettleUnits: 0, IdemKey: tag + "-settled-idem"},
		{BatchID: tag + "-released", APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
			Provider: "deepseek", Status: "failed", SettleStatus: "released",
			ReserveUnits: 0, SettleUnits: 0, IdemKey: tag + "-released-idem"},
	}
	for _, r := range rows {
		helper.InsertBatchTask(t, db, r)
		defer helper.DeleteBatchTask(t, db, r.BatchID)
	}

	// 键存在性必须走 map 的 ok 判定：结构体解码会给缺席键填零值，
	// "值正确"断言无法发现键缺席（本 issue 的缺陷形态正是键缺席）。
	intZeroKeys := []string{"est_lines", "usage_input_tokens", "usage_output_tokens",
		"reserve_units", "settle_units"}
	strEmptyKeys := []string{"output_file_id", "terminal_at"}
	rawZeroTokens := []string{`"est_lines":0`, `"usage_input_tokens":0`,
		`"usage_output_tokens":0`, `"reserve_units":0`, `"settle_units":0`,
		`"output_file_id":""`, `"terminal_at":""`}

	for _, r := range rows {
		resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + r.BatchID)
		require.NoError(t, err)
		testutil.AssertSuccess(t, resp)

		var data map[string]interface{}
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		for _, k := range intZeroKeys {
			v, ok := data[k]
			require.True(t, ok, "键 %s 必须显式出现（batch %s, settle=%s）", k, r.BatchID, r.SettleStatus)
			assert.Equal(t, float64(0), v, "键 %s 的值必须为 0", k)
		}
		for _, k := range strEmptyKeys {
			v, ok := data[k]
			require.True(t, ok, "键 %s 必须显式出现（batch %s, settle=%s）", k, r.BatchID, r.SettleStatus)
			assert.Equal(t, "", v, "键 %s 的值必须为空串", k)
		}
		// usage_source 契约缺口（枚举无空值）：维持省略，锁现行为
		_, ok := data["usage_source"]
		assert.False(t, ok, "usage_source 空值时键必须省略（契约缺口，现行为）")

		// 原始报文 token 精确匹配（防解码容忍造成假通过；整数字段
		// 不得出现小数点/字符串化）
		body := string(resp.RawBody)
		for _, token := range rawZeroTokens {
			assert.Contains(t, body, token, "原始报文必须含 %s", token)
		}
		assert.NotContains(t, body, `"terminal_at":null`, "terminal_at 只允许空串，不允许 null")
	}
}

// ---------- BT-2-007 非零回归：金额按定点整数输出、terminal_at 为 RFC3339（issue #271） ----------

// TestBatchDetail_NonZeroValues 防"无条件输出 0/空串"的过修回归：金额
// 非零时原样输出（1e-8 定点整数，不 ÷1e8），terminal_at 非空时为
// RFC3339 字符串。本用例修复前后都应通过。
func TestBatchDetail_NonZeroValues(t *testing.T) {
	helper.RequireBatchAPI(t)
	db := helper.OpenDB(t, sm)

	tag := helper.Unique("bt-2-007")
	terminalAt := time.Date(2026, 10, 6, 10, 20, 0, 0, time.UTC)
	helper.InsertBatchTask(t, db, helper.BatchTaskRow{
		BatchID: tag, APIKeyID: tag + "-ak", ProductName: helper.TestProductName,
		Provider: "deepseek", Status: "completed", SettleStatus: "settled",
		ReserveUnits: 250000000, SettleUnits: 180000000, // 2.5 / 1.8 RMB
		OutputFileID: tag + "-out", TerminalAt: &terminalAt, IdemKey: tag + "-idem",
	})
	defer helper.DeleteBatchTask(t, db, tag)

	resp, err := testutil.GetClient().Get("/open-api/v1/batches/" + tag)
	require.NoError(t, err)
	testutil.AssertSuccess(t, resp)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.Equal(t, float64(180000000), data["settle_units"])
	assert.Equal(t, "2026-10-06T10:20:00Z", data["terminal_at"])
	assert.Equal(t, tag+"-out", data["output_file_id"])

	// 原始报文整数 token 精确匹配（沿用 BT-2-002 的断言法）
	needle := strconv.Quote("settle_units") + ":"
	idx := strings.LastIndex(string(resp.RawBody), needle)
	require.NotEqual(t, -1, idx)
	rest := string(resp.RawBody)[idx+len(needle):]
	end := strings.IndexAny(rest, ",}")
	require.NotEqual(t, -1, end)
	token := strings.TrimSpace(rest[:end])
	assert.Equal(t, "180000000", token, "settle_units must serialize as integer token")
}
