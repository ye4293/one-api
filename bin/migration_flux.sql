-- Flux API 集成数据库迁移脚本
-- 为 images 表添加自增主键 id、updated_at 和 total_duration 字段

-- 1. 添加自增主键 id（如果不存在）
-- 注意：MySQL 不支持 IF NOT EXISTS 语法，使用存储过程处理
DELIMITER $$

CREATE PROCEDURE AddIdColumnIfNotExists()
BEGIN
    DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;

    -- 添加 id 列作为主键
    ALTER TABLE images
        ADD COLUMN id BIGINT AUTO_INCREMENT PRIMARY KEY FIRST;
END$$

DELIMITER ;

CALL AddIdColumnIfNotExists();
DROP PROCEDURE IF EXISTS AddIdColumnIfNotExists;

-- 2. 为 task_id 添加唯一索引（如果不存在）
DELIMITER $$

CREATE PROCEDURE AddTaskIdIndexIfNotExists()
BEGIN
    DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;

    -- 为 task_id 添加唯一索引
    ALTER TABLE images
        ADD UNIQUE INDEX idx_task_id (task_id);
END$$

DELIMITER ;

CALL AddTaskIdIndexIfNotExists();
DROP PROCEDURE IF EXISTS AddTaskIdIndexIfNotExists;

-- 3. 添加 updated_at 字段（如果不存在）
DELIMITER $$

CREATE PROCEDURE AddUpdatedAtColumnIfNotExists()
BEGIN
    DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;

    -- 添加 updated_at 字段（使用 BIGINT 存储 Unix 时间戳）
    ALTER TABLE images
        ADD COLUMN updated_at BIGINT DEFAULT 0;
END$$

DELIMITER ;

CALL AddUpdatedAtColumnIfNotExists();
DROP PROCEDURE IF EXISTS AddUpdatedAtColumnIfNotExists;

-- 4. 添加 total_duration 字段（如果不存在）
DELIMITER $$

CREATE PROCEDURE AddTotalDurationColumnIfNotExists()
BEGIN
    DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;

    -- 添加 total_duration 字段（总时长，单位：秒）
    ALTER TABLE images
        ADD COLUMN total_duration INT DEFAULT 0;
END$$

DELIMITER ;

CALL AddTotalDurationColumnIfNotExists();
DROP PROCEDURE IF EXISTS AddTotalDurationColumnIfNotExists;

-- 5. 添加 result 字段（如果不存在）
DELIMITER $$

CREATE PROCEDURE AddResultColumnIfNotExists()
BEGIN
    DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;

    -- 添加 result 字段（存储 API 响应结果的 JSON）
    ALTER TABLE images
        ADD COLUMN result TEXT;
END$$

DELIMITER ;

CALL AddResultColumnIfNotExists();
DROP PROCEDURE IF EXISTS AddResultColumnIfNotExists;

-- 迁移完成
SELECT 'Flux migration completed successfully' AS status;
