# 升级指南

本文档描述如何从一个已经部署的较早版本进行升级。

## v0.0.11

### 升级路径

可以从如下版本升级至v0.0.11:

- v0.0.10

### 升级步骤

1. 获取 API Server 可执行程序，参考 [部署说明](./deploy.md)
2. 替换 ai-gateway-api 的可执行程序
3. mysql 数据库表结构更新

> 完整的 v0.0.10 → v0.0.11 增量升级脚本见 `database/db_ddl_upgrade_to_0.0.11.sql`（覆盖 EPP runtime、api_key_hash、k8s 实例池、批量任务、ai-cache/context/mirror/intent 等全部 schema 变更；`api_key_hash` 回填等手动步骤见脚本末尾）。新装库请直接执行 `db_ddl.sql`。下面仅以 ai-cache 语义缓存二期为例给出存量加列语句。

```
ALTER TABLE ai_cache_rules ADD COLUMN `enable_semantic_cache` tinyint(1) NOT NULL DEFAULT 0 COMMENT '是否启用语义缓存: 0-否, 1-是（二期）';

CREATE TABLE IF NOT EXISTS `ai_cache_semantic_settings` (
  `id` BIGINT AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID（恒为1语义，物理单行）',
  `top_k` INT NOT NULL DEFAULT 1 COMMENT '语义检索TopK（1-10）',
  `threshold` DOUBLE NOT NULL DEFAULT 0.15 COMMENT '相似度阈值（量纲与 threshold_relation 一致，0-2）',
  `threshold_relation` VARCHAR(8) NOT NULL DEFAULT 'lt' COMMENT '阈值比较: gt/gte/lt/lte',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI缓存语义全局设置表（单行）';
```

SQLite 部署对应语句（不支持 COMMENT/IF NOT EXISTS 以外的差异，ADD COLUMN 重复执行会报错，已执行过请跳过）：

```
ALTER TABLE ai_cache_rules ADD COLUMN enable_semantic_cache INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS ai_cache_semantic_settings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  top_k INTEGER NOT NULL DEFAULT 1,
  threshold REAL NOT NULL DEFAULT 0.15,
  threshold_relation TEXT NOT NULL DEFAULT 'lt',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TRIGGER IF NOT EXISTS ai_cache_semantic_settings_updated_at AFTER UPDATE ON ai_cache_semantic_settings
  FOR EACH ROW BEGIN UPDATE ai_cache_semantic_settings SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id; END;
```

## v0.0.2

### 升级路径

可以从如下版本升级至v0.0.2:

- v0.0.1

### 升级步骤

1. 获取 API Server 可执行程序，参考 [部署说明](./deploy.md) 
2. 替换 ai-gateway-api 的可执行程序
3. mysql 数据库表结构更新

```
ALTER TABLE users ADD COLUMN `type` tinyint(1) NOT NULL DEFAULT '0' AFTER name;
ALTER TABLE users ADD COLUMN `scopes` varchar(2048) NOT NULL DEFAULT '' AFTER `type`;

UPDATE users SET type = 0, scopes = 'System' WHERE roles = 'admin';
UPDATE users SET type = 1, scopes = 'Support' WHERE roles = 'inner';

ALTER TABLE users CHANGE COLUMN  `session_key`   `ticket` varchar(20) NOT NULL DEFAULT '';
ALTER TABLE users CHANGE COLUMN  `session_key_created_at`  `ticket_created_at` datetime NOT NULL DEFAULT '0000-01-01 00:00:00';

ALTER TABLE users DROP COLUMN `roles`;

ALTER TABLE users DROP INDEX  name_uni;
ALTER TABLE users ADD   UNIQUE KEY `name_uni` (`name`, `type`);
```

3. Dashboard 版本升级

请升级 Dashboard 到 v0.0.2 版本。

4. Conf-Agent 版本升级

需要 v0.0.1 或更新版本的 Conf-Agent 。

如果准备继续使用 v0.0.1 版本的 Conf-Agent , 请按如下方式编辑 `conf/conf-agent.toml`:

```
# old:
{"Authorization" = "Session {Token}"}

# now:
{"Authorization" = "Token {Token}"}
```