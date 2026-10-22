-- 已存在且包含 username 的 request_stats_minute 表升级；新库使用完整 schema 文件。
-- 先执行本 SQL，再启动新版 Worker；不修改 logs，不进入 AutoMigrate。
ALTER TABLE request_stats_minute
    ADD COLUMN max_log_id BIGINT UNSIGNED NOT NULL DEFAULT 0
        COMMENT '本分钟本维度最大日志ID；异常分钟保留全分钟最大ID，空分钟继承边界；旧汇总未回填为0'
        AFTER duration_max_us;

-- 旧汇总默认0；新版 Worker 重算/新增分钟时自动写入，无需新增其他表或全量回填。
-- 汇总 [10:02,10:03) 读取 bucket_start=10:00 的 MAX(max_log_id)。
-- 使用 id > 下界并走 PRIMARY，始终保留 created_at 的分钟半开区间条件。
-- 前两分钟无记录或最大ID为0时使用内存初始ID；仍为0时按主键二分定位。
-- 空汇总表读取当前 logs 最大ID与下一完整分钟，仅在内存初始化，不写额外记录。
-- 首批汇总前重启会重新初始化；已有汇总从 MIN/MAX(bucket_start) 恢复覆盖与进度。
-- 已结束的空分钟仍保存零计数占位和ID边界，避免无请求时进度丢失。
-- 数据异常分钟整分钟记零，保留该分钟统计范围内最大ID并继续推进；异常原因写应用日志。
-- 查询超时、连接或写入失败仍回滚重试；源数据修复后可重算覆盖零占位。
