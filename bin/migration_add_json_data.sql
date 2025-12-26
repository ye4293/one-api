-- ============================================
-- 添加 videos 表 json_data 字段
-- 版本: v1.2
-- 日期: 2025-12-26
-- 说明: 为 videos 表添加 json_data 字段保存 Kling 回调完整数据
-- ============================================

-- 使用数据库
USE `one-api`;

-- 步骤 1: 添加 json_data 字段
ALTER TABLE `videos` 
ADD COLUMN `json_data` TEXT COMMENT 'Kling回调的完整JSON数据';

-- 步骤 2: 验证字段已添加
SHOW COLUMNS FROM `videos` LIKE 'json_data';

-- 步骤 3: 查看示例数据
SELECT `id`, `task_id`, `status`, LENGTH(`json_data`) as json_length FROM `videos` LIMIT 5;

-- ============================================
-- 回滚脚本(如需回滚)
-- ============================================
-- ALTER TABLE `videos` DROP COLUMN `json_data`;

