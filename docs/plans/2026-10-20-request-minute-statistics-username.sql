-- 已存在 request_stats_minute 的 MySQL 8 数据库升级；新库使用完整 schema 文件。
-- 先暂停报表查询和全部 Worker，再执行。不是 AutoMigrate，不触碰 logs。
-- username 与 logs 的历史快照精确匹配，不以当前 users.username 回填历史。
ALTER TABLE request_stats_minute
    ADD COLUMN username VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin
        NOT NULL DEFAULT '' COMMENT '日志用户名历史快照；NULL归空串，同一用户改名分组，ID查询合并历史名称' AFTER user_id,
    DROP INDEX uk_rsm_bucket_dims,
    ADD UNIQUE KEY uk_rsm_bucket_dims (bucket_start, user_id, username, token_name, model_name, channel_id),
    ADD KEY idx_rsm_username_time (username, bucket_start);

-- 旧汇总的 username 初始为空，尚不能按名称查询历史真实数据。
-- 部署新版后，先保持报表查询关闭，使用新版 RequestStatistics.Recompute 逐段
-- 重算仍保留源日志的历史分钟（每段建议 <= 1 小时，所有主节点共用现有连接锁）。
-- Recompute 复用逐分钟事务替换，按用户名拆分同一用户改名前后的日志；ID 查询总量不变。
-- 只有核对真实数据已重算完成，才开放按用户名查询。无法补算的区间不能标为完整覆盖。
-- 空分钟占位 user_id=0 的 username 保持空串，无需回填。
