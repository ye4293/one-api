-- ============================================
-- Kling API 双主键方案数据库变更脚本
-- 版本: v1.0
-- 日期: 2025-12-26
-- 说明: 为 videos 表添加自增主键 id 并优化索引
-- ============================================

-- 使用数据库
USE `one-api`;

-- 步骤 1: 备份现有 videos 表(可选但强烈推荐)
-- CREATE TABLE `videos_backup_20251226` LIKE `videos`;
-- INSERT INTO `videos_backup_20251226` SELECT * FROM `videos`;

-- 步骤 2: 检查表是否存在
SELECT 
    TABLE_NAME,
    TABLE_ROWS,
    DATA_LENGTH,
    INDEX_LENGTH
FROM 
    information_schema.TABLES 
WHERE 
    TABLE_SCHEMA = 'one-api' 
    AND TABLE_NAME = 'videos';

-- 步骤 3: 如果 videos 表不存在,创建新表(带双主键方案)
CREATE TABLE IF NOT EXISTS `videos` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键,用于高效范围查询',
    `task_id` VARCHAR(200) NOT NULL COMMENT '业务任务ID,由系统生成',
    `prompt` TEXT COMMENT '用户提示词',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间戳',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间戳',
    `type` VARCHAR(50) DEFAULT '' COMMENT '任务类型: text2video/omni-video/image2video/multi-image2video',
    `provider` VARCHAR(50) DEFAULT '' COMMENT '服务提供商: kling/luma/runway等',
    `mode` VARCHAR(50) DEFAULT '' COMMENT '模式',
    `duration` VARCHAR(20) DEFAULT '' COMMENT '视频时长',
    `resolution` VARCHAR(20) DEFAULT '' COMMENT '视频分辨率',
    `username` VARCHAR(100) DEFAULT '' COMMENT '用户名',
    `channel_id` INT NOT NULL DEFAULT 0 COMMENT '渠道ID',
    `user_id` INT NOT NULL DEFAULT 0 COMMENT '用户ID',
    `model` VARCHAR(100) DEFAULT '' COMMENT '模型名称',
    `status` VARCHAR(50) DEFAULT 'pending' COMMENT '任务状态: pending/submitted/processing/succeed/failed',
    `fail_reason` TEXT COMMENT '失败原因',
    `video_id` VARCHAR(200) DEFAULT '' COMMENT '第三方API返回的视频ID',
    `store_url` TEXT COMMENT '视频存储URL',
    `quota` BIGINT NOT NULL DEFAULT 0 COMMENT '消耗额度',
    `n` INT NOT NULL DEFAULT 1 COMMENT '生成数量',
    `credentials` TEXT COMMENT '任务创建时使用的凭证JSON',
    `json_data` TEXT COMMENT 'Kling回调的完整JSON数据',
    
    -- 主键和索引
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_task_id` (`task_id`(40)),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_channel_id` (`channel_id`),
    KEY `idx_status` (`status`),
    KEY `idx_video_id` (`video_id`(40))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频生成任务表';

-- 步骤 4: 如果表已存在但没有 id 字段,执行以下变更
-- 注意: 以下语句需要根据实际情况调整

-- 4.1 添加自增主键列(如果不存在)
ALTER TABLE `videos` 
ADD COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT FIRST,
ADD PRIMARY KEY (`id`);

-- 4.2 删除旧的 idx_tid 索引(如果存在)
ALTER TABLE `videos` DROP INDEX IF EXISTS `idx_tid`;

-- 4.3 将 task_id 改为唯一索引并设置 NOT NULL
ALTER TABLE `videos` 
MODIFY COLUMN `task_id` VARCHAR(200) NOT NULL COMMENT '业务任务ID,由系统生成',
ADD UNIQUE INDEX `idx_task_id` (`task_id`(40));

-- 4.4 添加 updated_at 字段
ALTER TABLE `videos` 
ADD COLUMN IF NOT EXISTS `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间戳' AFTER `created_at`;

-- 4.5 添加 json_data 字段
ALTER TABLE `videos`
ADD COLUMN IF NOT EXISTS `json_data` TEXT COMMENT 'Kling回调的完整JSON数据';

-- 4.6 优化其他索引
ALTER TABLE `videos` 
ADD INDEX IF NOT EXISTS `idx_user_id` (`user_id`),
ADD INDEX IF NOT EXISTS `idx_channel_id` (`channel_id`),
ADD INDEX IF NOT EXISTS `idx_status` (`status`),
ADD INDEX IF NOT EXISTS `idx_video_id` (`video_id`(40));

-- 步骤 5: 验证变更结果
SHOW CREATE TABLE `videos`;

-- 步骤 6: 查看索引信息
SHOW INDEX FROM `videos`;

-- 步骤 7: 统计表信息
SELECT 
    COUNT(*) as total_records,
    COUNT(DISTINCT task_id) as unique_task_ids,
    MIN(created_at) as earliest_record,
    MAX(created_at) as latest_record
FROM `videos`;

-- ============================================
-- 回滚脚本(如需回滚,请谨慎执行)
-- ============================================
-- ALTER TABLE `videos` DROP PRIMARY KEY;
-- ALTER TABLE `videos` DROP COLUMN `id`;
-- ALTER TABLE `videos` DROP INDEX `idx_task_id`;
-- ALTER TABLE `videos` ADD INDEX `idx_tid` (`task_id`(40));
-- -- 恢复备份数据
-- TRUNCATE TABLE `videos`;
-- INSERT INTO `videos` SELECT * FROM `videos_backup_20251226`;

