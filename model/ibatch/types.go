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

// Package ibatch 实现批量与异步任务支持（一期）控制面模型层：批量任务
// （batch_tasks）与批量文件（batch_files）的查询、cancel、Redis 同步落库、
// 状态推进与三态对账（预留-结算-释放）。对账 job 的循环框架与装配见
// design-docs/sys-design/details/批量任务与对账.md。
package ibatch

import (
	"time"
)

// 批量任务状态机（对齐 OpenAI Batch API；provider 返回状态原样存储、
// 终态不回退；二期方言 Anthropic ended / Gemini deleted 为枚举增量）。
const (
	TaskStatusValidating = "validating"
	TaskStatusQueued     = "queued"
	TaskStatusInProgress = "in_progress"
	TaskStatusFinalizing = "finalizing"
	TaskStatusCompleted  = "completed"
	TaskStatusExpired    = "expired"
	TaskStatusFailed     = "failed"
	TaskStatusCancelled  = "cancelled"
	TaskStatusCancelling = "cancelling"
)

// TerminalStatuses 是终态集合（cancelling 为受理中中间态，非终态）。
// cancel 抢占、状态推进、对账释放均以该集合判定。
var TerminalStatuses = []string{
	TaskStatusCompleted,
	TaskStatusExpired,
	TaskStatusFailed,
	TaskStatusCancelled,
}

// IsTerminalStatus 报告 status 是否为终态。
func IsTerminalStatus(status string) bool {
	for _, s := range TerminalStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// 三态对账（settle_status）主字段取值。
const (
	SettleStatusReserved = "reserved"
	SettleStatusSettled  = "settled"
	SettleStatusReleased = "released"
)

// 用量来源（usage_source）。
const (
	UsageSourceDownload  = "download"
	UsageSourceReconcile = "reconcile"
)

// 同步来源（sync_source）：redis 为主事件源同步落库；log 为日志补登。
const (
	SyncSourceRedis = "redis"
	SyncSourceLog   = "log"
)

// 批量文件方向（batch_files.direction / Redis BATCH_FILE 的 dir 字段）。
const (
	FileDirectionInput  = "input"
	FileDirectionOutput = "output"
)

// 任务实体（batch_tasks 行）。金额字段为 1e-8 定点整数、单位 RMB，
// 与数据面 QUOTA_* / quotacache 同口径。
type BatchTask struct {
	ID                int64            `json:"id"`
	BatchID           string           `json:"batch_id"`
	APIKeyID          string           `json:"api_key_id"`
	ProductName       string           `json:"product_name"`
	EntityID          string           `json:"entity_id,omitempty"`
	Provider          string           `json:"provider"`
	KeyName           string           `json:"key_name,omitempty"`
	Endpoint          string           `json:"endpoint,omitempty"`
	InputFileID       string           `json:"input_file_id,omitempty"`
	OutputFileID      string           `json:"output_file_id,omitempty"`
	Status            string           `json:"status"`
	RequestCounts     map[string]int64 `json:"request_counts,omitempty"`
	EstLines          int64            `json:"est_lines,omitempty"`
	UsageInputTokens  int64            `json:"usage_input_tokens,omitempty"`
	UsageOutputTokens int64            `json:"usage_output_tokens,omitempty"`
	UsageSource       string           `json:"usage_source,omitempty"`
	ReserveUnits      int64            `json:"reserve_units,omitempty"`
	SettleUnits       int64            `json:"settle_units,omitempty"`
	SettleStatus      string           `json:"settle_status"`
	OverReserved      bool             `json:"over_reserved"`
	SyncSource        string           `json:"sync_source"`
	IdemKey           string           `json:"idem_key"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	TerminalAt        *time.Time       `json:"terminal_at,omitempty"`

	// BatchFiles 为详情接口对外组装的关联文件元数据视图（batches.md
	// §3.2，元素形状见 §2.2）。非持久化字段，DAO 不读写；列表接口不填充
	//（§3.1 示例不含 batch_files）。
	BatchFiles []*BatchFileMeta `json:"batch_files,omitempty"`
}

// BatchFileMeta 是批量文件元数据的对外视图（batches.md §2.2）：只映射
// 契约字段，内部字段（id / key_name / api_key_id / product_name /
// direction / lines / first_seen_at / last_seen_at / updated_at）不外泄。
// purpose 由文件方向映射为契约枚举（batch_files.purpose 列保留 provider
// 原值，如 "batch"）；filename 来自 provider File 对象，缺失时省略。
type BatchFileMeta struct {
	FileID    string    `json:"file_id"`
	Provider  string    `json:"provider"`
	Purpose   string    `json:"purpose,omitempty"`
	Filename  string    `json:"filename,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ToMeta 将文件实体转换为对外视图。
func (f *BatchFile) ToMeta() *BatchFileMeta {
	if f == nil {
		return nil
	}
	return &BatchFileMeta{
		FileID:    f.FileID,
		Provider:  f.Provider,
		Purpose:   f.contractPurpose(),
		Filename:  f.Filename,
		Bytes:     f.Bytes,
		CreatedAt: f.CreatedAt,
	}
}

// contractPurpose 把文件方向映射为契约 purpose（batches.md §2.2）：
// input → batch_input、output → batch_output。direction 缺失（历史脏
// 数据）时透传列值，不猜方向。
func (f *BatchFile) contractPurpose() string {
	switch f.Direction {
	case FileDirectionInput:
		return PurposeBatchInput
	case FileDirectionOutput:
		return PurposeBatchOutput
	}
	return f.Purpose
}

// 批量文件契约 purpose（batches.md §2.2）。
const (
	PurposeBatchInput  = "batch_input"
	PurposeBatchOutput = "batch_output"
)

// 文件实体（batch_files 行）。表内不存文件内容。
type BatchFile struct {
	ID          int64      `json:"id"`
	FileID      string     `json:"file_id"`
	Provider    string     `json:"provider"`
	KeyName     string     `json:"key_name,omitempty"`
	APIKeyID    string     `json:"api_key_id"`
	ProductName string     `json:"product_name"`
	Direction   string     `json:"direction"`
	Purpose     string     `json:"purpose,omitempty"`
	Filename    string     `json:"filename,omitempty"`
	Lines       int64      `json:"lines,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// BatchTaskFilter 任务查询过滤条件。ListTasks 中 ProductName 为强制条件
// （服务端按默认产品线解析，不接受外部传参）；CursorID/Limit 构成主键
// 游标分页。
type BatchTaskFilter struct {
	ID           *int64
	BatchID      *string
	IdemKey      *string
	APIKeyID     *string
	ProductName  *string
	EntityID     *string
	Provider     *string
	Status       *string
	StatusIn     []string
	StatusNotIn  []string
	SettleStatus *string
	CreatedStart *time.Time
	CreatedEnd   *time.Time

	// CursorID：主键游标（id > CursorID），第一页为 nil。
	CursorID *int64
	// Limit：每页条数（默认 DefaultListLimit，上限 MaxListLimit）。
	Limit int
}

// BatchFileFilter 文件查询过滤条件。
type BatchFileFilter struct {
	ID          *int64
	FileID      *string
	Provider    *string
	APIKeyID    *string
	ProductName *string
	Direction   *string
}

// BatchTaskListResult 列表查询结果。NextCursor 为 0 表示无更多页。
type BatchTaskListResult struct {
	Tasks      []*BatchTask `json:"tasks"`
	NextCursor int64        `json:"next_cursor"`
}

// 列表分页默认与上限（api-changes.md §4.1）。
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// BatchTaskProgressPatch 状态推进的部分更新（nil 字段不更新）。
type BatchTaskProgressPatch struct {
	Status        *string
	OutputFileID  *string
	Endpoint      *string
	RequestCounts *map[string]int64
	TerminalAt    *time.Time
}

// BatchSettlePatch 三态对账条件更新的写入内容（nil 字段不更新）。
// SettleStatus 必填（settled / released）。
type BatchSettlePatch struct {
	SettleStatus      string
	SettleUnits       *int64
	UsageInputTokens  *int64
	UsageOutputTokens *int64
	UsageSource       *string
	OverReserved      *bool
	TerminalAt        *time.Time
}
