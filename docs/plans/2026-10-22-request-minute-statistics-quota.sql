-- 已包含 username、max_log_id 的 request_stats_minute 表升级；新库使用完整 schema 文件。
-- 先关闭全部 Worker，执行本 SQL，再启动新版 Worker；不修改 logs，不进入 AutoMigrate。
ALTER TABLE request_stats_minute
    ADD COLUMN used_quota DECIMAL(30,0) NULL DEFAULT NULL
        COMMENT '本分钟本维度logs.quota之和，NULL源值按0，保留负数；空分钟为0；NULL表示旧汇总尚未回填'
        AFTER total_tokens,
    ADD COLUMN used_quota_usd DECIMAL(31,6) NULL DEFAULT NULL
        COMMENT 'used_quota除以500000的美元金额，精确保存6位小数；空分钟为0；NULL表示旧汇总尚未回填'
        AFTER used_quota;

-- 新版 Worker 重算或新增分钟时，同时写入两个字段。
-- 旧汇总保留 NULL，不能将尚未回填解释为零用量；本 SQL 不扫描日志或自动补历史。
-- 如需历史值，源日志仍完整保留的已完成分钟可通过 RequestStatistics.Recompute 分段重算。
-- 用量沿用现有 user_id>0、type IN (2,5)、created_at 与 ID 边界、用户名和原四维分组。
-- 金额固定使用汇总用量/500000，六位小数；NULL源quota按0，负数按原值参与求和。
