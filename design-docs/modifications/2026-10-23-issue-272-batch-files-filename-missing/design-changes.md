# Issue #272：design-changes 说明

对应 `change-summary.md`。本次修复属"落库行为对齐既有条款"，设计文档变更范围如下。

## 1. api-define（OpenAPI 接口定义）

`design-docs/api-define/OpenAPI接口定义/batches.md` §2.2 **不改**：

- 条款"filename 为 provider File 对象给出的文件名（数据面同步落库）；缺失时（老键、provider 未给）
  为空，响应中省略该键"本身已覆盖本场景（provider 已给 → 必现），条款是 Oracle，缺陷在运行行为；
- §2.2 示例（第 106-114 行）已显式给出 `"filename": "input.jsonl"`，无需补示例。

## 2. sys-design（系统设计）

`design-docs/sys-design/details/批量任务与对账.md`（实施时落地）：

- 对账落库小节补一条维护性注记：`TBatchFileUpsertByFileProvider` 是 `batch_files` 唯一生产写入路径，
  其 INSERT 列清单 / 冲突更新列表必须与 `TBatchFileParam` 字段保持同步；新增字段时两处同改，
  防止再次出现"参数有值、SQL 丢列"的静默丢弃（本 issue 根因模式）。
