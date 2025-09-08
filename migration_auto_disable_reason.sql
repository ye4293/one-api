-- 添加自动禁用原因字段的数据库迁移脚本
-- 执行时间：请在部署新版本前执行此脚本

-- 为单Key渠道添加自动禁用原因和时间字段
ALTER TABLE channels ADD COLUMN auto_disabled_reason TEXT NULL COMMENT '自动禁用原因';
ALTER TABLE channels ADD COLUMN auto_disabled_time BIGINT NULL COMMENT '自动禁用时间戳';

-- 创建索引以提高查询性能
CREATE INDEX idx_channels_auto_disabled_time ON channels(auto_disabled_time);
CREATE INDEX idx_channels_auto_disabled_reason ON channels(auto_disabled_reason(255));

-- 更新说明
-- 1. auto_disabled_reason: 存储自动禁用的详细原因
-- 2. auto_disabled_time: 存储禁用的时间戳（Unix时间戳）
-- 3. 多Key渠道的每个Key的禁用原因存储在 multi_key_info JSON字段的 KeyMetadata 中
-- 4. 新增字段允许NULL值，兼容现有数据

-- 验证脚本执行结果
-- SELECT * FROM information_schema.columns WHERE table_name = 'channels' AND column_name IN ('auto_disabled_reason', 'auto_disabled_time');
