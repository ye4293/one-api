-- 新库 DDL：仅新增分钟汇总表。MySQL 8 / 兼容 PolarDB，InnoDB。
-- 既有表按缺失字段依次执行 username、max-log-id 和 quota 升级 SQL。
-- 不新增 logs 字段，不进入 AutoMigrate。
-- Token 名称直接取现有 logs.token_name；源时间直接使用 logs.created_at；核对现有范围索引。
-- token_name 长度与 NO PAD 排序规则须核对目标库支持情况，禁止截断源名称。
-- 全站空分钟仅写一条占位：user_id=0、username=''、token_name=''、model_name=''、channel_id=0，计数和总耗时为0，极值为NULL。
-- 数据校验异常或分组超过50000时，整分钟写同格式零占位，max_log_id 保存本分钟统计范围内的最大日志ID。
-- 异常原因写应用日志，继续下一分钟；数据库超时/读写失败仍回滚重试，不作为零流量发布。
-- 修复源数据后可重算覆盖零占位；异常分钟与真实空分钟使用相同记录格式。
-- 有请求时只写实际用户名称及原四维分组；报表与维度候选查询始终添加 user_id > 0。
-- 不写初始化记录；MIN/MAX(bucket_start) 从实际汇总（包含已结束的空分钟）恢复覆盖和进度。

CREATE TABLE request_stats_minute (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '汇总记录自增主键，不作为业务去重依据',
    bucket_start BIGINT UNSIGNED NOT NULL COMMENT '已结束分钟的起点，UTC Unix秒且为60的倍数',
    user_id BIGINT UNSIGNED NOT NULL COMMENT '真实统计用户ID大于0；0保留给空分钟或异常分钟零占位，报表排除',
    username VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '' COMMENT '日志用户名历史快照；NULL归空串，同一用户改名分组，ID查询合并历史名称',
    token_name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '' COMMENT '取logs.token_name历史快照；NULL归空串，同用户同名合并，改名分组；空分钟占位为空串',
    model_name VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT '日志记录的模型名，区分大小写；真实统计非空，空分钟占位为空串',
    channel_id BIGINT UNSIGNED NOT NULL COMMENT '日志记录的渠道ID；允许0，空分钟占位也为0',
    total_requests BIGINT UNSIGNED NOT NULL COMMENT '本分钟本维度type为2或5的日志行数，不按请求标识去重；空分钟占位为0',
    total_tokens DECIMAL(30,0) NOT NULL COMMENT '消费与错误日志已记录输入与输出Token之和，NULL分项按0；不额外加缓存Token；空分钟为0，用于查询时计算TPM',
    used_quota DECIMAL(30,0) NULL DEFAULT NULL COMMENT '本分钟本维度logs.quota之和，NULL源值按0，保留负数；空分钟为0；NULL表示旧汇总尚未回填',
    used_quota_usd DECIMAL(31,6) NULL DEFAULT NULL COMMENT 'used_quota除以500000的美元金额，精确保存6位小数；空分钟为0；NULL表示旧汇总尚未回填',
    success_count BIGINT UNSIGNED NOT NULL COMMENT '消费日志数（type=2），与失败数之和等于总请求数',
    failure_count BIGINT UNSIGNED NOT NULL COMMENT '冗余保存的错误日志数（type=5），与成功数在同一次重算中写入',
    duration_count BIGINT UNSIGNED NOT NULL COMMENT '已记录且换算后耗时大于0微秒的请求数，包含成功和失败；平均耗时以此为分母，缺失或0耗时不计入',
    duration_sum_us DECIMAL(30,0) NOT NULL COMMENT '有效请求耗时之和，单位微秒；无有效样本时为0，用于跨桶加权计算平均值',
    duration_min_us BIGINT UNSIGNED NULL COMMENT '有效请求最小耗时，单位微秒；无有效样本时为NULL',
    duration_max_us BIGINT UNSIGNED NULL COMMENT '有效请求最大耗时，单位微秒；无有效样本时为NULL',
    max_log_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '本分钟本维度最大日志ID；异常分钟保留全分钟最大ID，空分钟继承边界；旧汇总未回填为0',
    calculated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '本行重算写入时间，精确到微秒；不代表源数据已最终完整',
    PRIMARY KEY (id),
    UNIQUE KEY uk_rsm_bucket_dims
        (bucket_start, user_id, username, token_name, model_name, channel_id),
    KEY idx_rsm_user_time (user_id, bucket_start),
    KEY idx_rsm_username_time (username, bucket_start),
    KEY idx_rsm_token_time (token_name, bucket_start),
    KEY idx_rsm_model_time (model_name, bucket_start),
    KEY idx_rsm_channel_time (channel_id, bucket_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
  COMMENT='含用户名快照的分钟统计，按时间顺序整分钟事务替换，全站空分钟写一条零占位';

-- rpm/tpm 为响应派生字段，不持久化速率；本表含 used_quota、used_quota_usd 共19个字段。
-- 汇总 [t,t+60) 时，取 bucket_start=t-120 的 MAX(max_log_id)，首次运行下界不低于内存初始ID。
-- 每个维度的 MAX(logs.id) 随聚合计算并事务写入；空分钟沿用上一分钟或已定位的边界。
-- 所有分钟均保留 id > 下界和时间条件，并走 PRIMARY；不再回退为纯时间查询。
-- ID 下界仅提供约一分钟乱序回扫余量，不保证覆盖任意延迟写入或历史修订。
-- total_tokens 从同一统计范围内的 logs.prompt_tokens + logs.completion_tokens 聚合，先转DECIMAL再相加。
-- 仅表达日志已记录Token量，缺失usage不推估、不表示真实消耗为0；旧Token字段和计费逻辑不修改。
-- used_quota 从相同日志范围求和，used_quota_usd 固定除以500000，不使用可变 QuotaPerUnit。
-- 用量和金额不经过浮点数；DECIMAL(31,6) 可容纳30位整数用量换算后的金额。
-- 空分钟或数据异常零占位的用量与金额都写0；历史NULL字段仅通过整分钟重算回填。

-- 无新增日志列或终态采集迁移。Worker和失败明细按created_at半开区间读取。
-- 上线前检查现有created_at范围索引及列表排序，例如(created_at,id)，必要时另行评估在线DDL。
-- duration原值单位为秒，读取时ROUND(duration*1000000)量化为整数微秒，不更新源字段。
-- 有效条件：duration>=0.000001 AND duration<9223372036854；其余不计耗时样本，保留行数。

-- 首次启动：主节点持有 Worker 锁，空汇总表读取 logs 主键端点初始化，不补历史。
-- 统计起点为下一完整分钟，初始ID与起点仅保存在 Worker 内存；等待期间不写汇总行。
-- 首批汇总前重启或切主，会重新取当前最大ID初始化；首批完成后从汇总表恢复，不重置进度。
-- 覆盖起点取仍保留的 MIN(bucket_start)，不再读取 REQUEST_STATS_COVERAGE_START。
-- 所有节点默认提供报表查询；仅 NODE_TYPE=master 且 REQUEST_STATS_WORKER_ENABLED=true 的节点启动聚合。
-- 不再读取 REQUEST_STATS_ENABLED、REQUEST_STATS_SOURCE_RETAINED_FROM、REQUEST_STATS_COVERAGE_VERIFIED。
-- 旧版升级沿用最早已有汇总分钟，并从原最新分钟续跑，不创建状态表或额外记录。
-- 旧汇总没有可用 max_log_id 时，通过主键二分定位前两分钟区间的结束时间；定位和聚合使用同一快照。
-- 查询错误直接重试，不用当前最大ID覆盖历史边界；重算/新增分钟后自动保存可复用的 max_log_id。
