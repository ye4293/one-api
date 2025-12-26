-- ============================================
-- 添加 videos 表 updated_at 字段
-- 版本: v1.1
-- 日期: 2025-12-26
-- 说明: 为 videos 表添加 updated_at 时间戳字段
-- ============================================

-- 使用数据库
USE `one-api`;

-- 步骤 1: 添加 updated_at 字段
ALTER TABLE `videos` 
ADD COLUMN `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间戳' AFTER `created_at`;

-- 步骤 2: 初始化现有数据的 updated_at (设置为 created_at)
UPDATE `videos` SET `updated_at` = `created_at` WHERE `updated_at` = 0;

-- 步骤 3: 验证字段已添加
SHOW COLUMNS FROM `videos` LIKE 'updated_at';

-- 步骤 4: 查看示例数据
SELECT `id`, `task_id`, `created_at`, `updated_at`, `status` FROM `videos` LIMIT 5;

-- ============================================
-- 回滚脚本(如需回滚)
-- ============================================
-- ALTER TABLE `videos` DROP COLUMN `updated_at`;

