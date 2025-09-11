-- 数据库迁移脚本：升级channels表key字段类型
-- 解决VertexAI多key存储长度限制问题
-- 执行日期：2024-12-19
-- 说明：将key字段从TEXT升级为MEDIUMTEXT以支持更大的JSON存储

-- 检查当前key字段类型
SELECT 
    COLUMN_NAME, 
    DATA_TYPE, 
    CHARACTER_MAXIMUM_LENGTH 
FROM 
    INFORMATION_SCHEMA.COLUMNS 
WHERE 
    TABLE_SCHEMA = DATABASE() 
    AND TABLE_NAME = 'channels' 
    AND COLUMN_NAME = 'key';

-- 升级key字段类型为MEDIUMTEXT
ALTER TABLE channels MODIFY COLUMN `key` MEDIUMTEXT;

-- 验证升级结果
SELECT 
    COLUMN_NAME, 
    DATA_TYPE, 
    CHARACTER_MAXIMUM_LENGTH 
FROM 
    INFORMATION_SCHEMA.COLUMNS 
WHERE 
    TABLE_SCHEMA = DATABASE() 
    AND TABLE_NAME = 'channels' 
    AND COLUMN_NAME = 'key';

-- 添加注释
ALTER TABLE channels MODIFY COLUMN `key` MEDIUMTEXT COMMENT 'API密钥，支持VertexAI多key存储';
