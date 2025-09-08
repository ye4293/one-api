-- MIGRATION TO ADD AUTO_DISABLED_MODEL TO CHANNELS TABLE

-- Add auto_disabled_model field to store the model name that caused the channel to be disabled.
ALTER TABLE channels ADD COLUMN auto_disabled_model VARCHAR(255) NULL COMMENT '导致自动禁用的模型名称';
