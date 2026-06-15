#!/usr/bin/env python3
"""
Prompt Caching 测试脚本
测试三种场景：缓存写入、独立请求缓存读取、多轮续写缓存读取
"""

import json
import time
import requests

API_BASE = "https://api.ezlinkai.com/v1/messages"
API_KEY = "dfoc9bnUFJZGf1Z57fC2D4Af93864e0f93890f3f5458E7Ec"

MODELS = [
    "claude-haiku-4-5",
    "claude-opus-4-5",
    "claude-opus-4-6",
    "claude-opus-4-7",
    "claude-opus-4-8",
    "claude-sonnet-4-5",
    "claude-sonnet-4-6",
]

# 足够长的 system prompt（需超过 1024 token 才能触发缓存）
LONG_SYSTEM_PROMPT = """你是一个专业的技术文档助手，专注于以下核心能力：

核心能力一：代码审查与分析
- 你能深入分析代码结构，找出潜在的性能瓶颈、安全漏洞和设计缺陷
- 你熟悉多种编程语言的最佳实践，包括但不限于 Python、Go、JavaScript、Rust
- 你能够提供具体的重构建议，并解释每个建议背后的设计原则

核心能力二：系统架构设计
- 你理解分布式系统的核心概念：CAP 定理、最终一致性、分区容错
- 你能设计高可用、高性能的微服务架构
- 你熟悉常见的架构模式：事件驱动、CQRS、Saga 模式、领域驱动设计

核心能力三：DevOps 与运维
- 你了解 CI/CD 流水线的最佳实践
- 你能配置 Kubernetes 集群、Helm Charts、Terraform 基础设施
- 你熟悉监控告警系统：Prometheus、Grafana、ELK Stack

核心能力四：数据库优化
- 你精通 SQL 和 NoSQL 数据库的性能调优
- 你能设计高效的索引策略和查询优化方案
- 你了解数据库分片、读写分离、连接池管理等高级主题

核心能力五：安全最佳实践
- 你了解 OWASP Top 10 安全风险及其防范措施
- 你能进行安全代码审计，识别常见漏洞（SQL 注入、XSS、CSRF 等）
- 你熟悉认证授权框架：OAuth 2.0、JWT、SAML

核心能力六：性能优化
- 你能分析应用性能瓶颈，提供针对性的优化建议
- 你了解缓存策略：Redis、Memcached、CDN 缓存
- 你能进行负载测试设计和结果分析

核心能力七：技术文档编写
- 你能编写清晰、结构化的 API 文档
- 你擅长将复杂的技术概念用通俗易懂的语言解释
- 你了解文档版本管理和协作最佳实践

核心能力八：项目管理
- 你了解敏捷开发方法论：Scrum、Kanban
- 你能帮助团队进行技术评估和风险分析
- 你能制定合理的技术路线图和里程碑计划

以上是你的八大核心能力。在每次对话中，请根据用户的问题，灵活运用这些能力来提供高质量的技术支持。
请始终保持专业、准确、简洁的回答风格。如果遇到不确定的问题，请明确说明并建议进一步验证的方向。

补充说明：你的回答需要考虑以下维度：
1. 正确性 - 确保技术细节准确无误
2. 完整性 - 覆盖问题的各个方面
3. 实用性 - 提供可直接应用的解决方案
4. 可维护性 - 建议的方案应易于长期维护
5. 可扩展性 - 考虑未来的扩展需求

你还需要关注以下最新技术趋势：
- AI/ML 在软件开发中的应用
- WebAssembly 的最新发展
- 边缘计算和 Serverless 架构
- 零信任安全模型
- 可观测性（Observability）的最佳实践"""

HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "anthropic-version": "2023-06-01",
    "content-type": "application/json",
}


def make_request(model, system_prompt, messages, label=""):
    """发送请求并返回 usage 信息"""
    payload = {
        "model": model,
        "max_tokens": 200,
        "system": [
            {
                "type": "text",
                "text": system_prompt,
                "cache_control": {"type": "ephemeral"},
            }
        ],
        "messages": messages,
    }

    try:
        resp = requests.post(API_BASE, headers=HEADERS, json=payload, timeout=120)
        data = resp.json()

        if resp.status_code != 200:
            error_msg = data.get("error", {}).get("message", resp.text[:200])
            return {"error": error_msg}

        usage = data.get("usage", {})
        content = ""
        for block in data.get("content", []):
            if block.get("type") == "text":
                content = block["text"]
                break

        return {
            "input_tokens": usage.get("input_tokens", 0),
            "output_tokens": usage.get("output_tokens", 0),
            "cache_creation_input_tokens": usage.get("cache_creation_input_tokens", 0),
            "cache_read_input_tokens": usage.get("cache_read_input_tokens", 0),
            "content_preview": content[:80],
        }
    except requests.exceptions.Timeout:
        return {"error": "请求超时 (120s)"}
    except Exception as e:
        return {"error": str(e)}


def test_model(model):
    """对单个模型执行三阶段测试"""
    print(f"\n{'='*70}")
    print(f"  模型: {model}")
    print(f"{'='*70}")

    # ── 第1步：缓存写入 ──
    print(f"\n  [1/3] 缓存写入...")
    msg1 = [{"role": "user", "content": "请记住上面的系统提示内容。"}]
    r1 = make_request(model, LONG_SYSTEM_PROMPT, msg1, "缓存写入")
    print_result("缓存写入", r1)

    if "error" in r1:
        print(f"  ⚠️  跳过后续测试（首次请求失败）")
        return

    first_reply = r1.get("content_preview", "OK")

    # 等待一下让缓存生效
    time.sleep(3)

    # ── 第2步：独立请求缓存读取（全新 messages，system 不变）──
    print(f"\n  [2/3] 独立请求缓存读取...")
    msg2 = [{"role": "user", "content": "你好，请列出你的第一条核心能力。"}]
    r2 = make_request(model, LONG_SYSTEM_PROMPT, msg2, "独立读取")
    print_result("独立缓存读取", r2)

    # 等待一下
    time.sleep(3)

    # ── 第3步：多轮续写缓存读取 ──
    print(f"\n  [3/3] 多轮续写缓存读取...")
    msg3 = [
        {"role": "user", "content": "请记住上面的系统提示内容。"},
        {
            "role": "assistant",
            "content": [{"type": "text", "text": first_reply}],
        },
        {"role": "user", "content": "基于你记住的内容，说出第一条核心能力。"},
    ]
    r3 = make_request(model, LONG_SYSTEM_PROMPT, msg3, "多轮续写")
    print_result("多轮续写缓存读取", r3)

    # ── 汇总 ──
    print(f"\n  {'─'*50}")
    print(f"  汇总: {model}")
    summarize(r1, r2, r3)


def print_result(label, result):
    if "error" in result:
        print(f"    ❌ {label}: 错误 - {result['error']}")
        return

    cache_create = result["cache_creation_input_tokens"]
    cache_read = result["cache_read_input_tokens"]
    input_tok = result["input_tokens"]

    status_create = "✅" if cache_create > 0 else "❌"
    status_read = "✅" if cache_read > 0 else "❌"

    print(f"    input={input_tok}, cache_creation={cache_create} {status_create}, cache_read={cache_read} {status_read}")
    print(f"    回复: {result['content_preview']}...")


def summarize(r1, r2, r3):
    def check(r, field):
        if "error" in r:
            return "💥"
        return "✅" if r.get(field, 0) > 0 else "❌"

    write = check(r1, "cache_creation_input_tokens")
    read_independent = check(r2, "cache_read_input_tokens")
    read_multi = check(r3, "cache_read_input_tokens")

    print(f"    缓存写入: {write}  |  独立请求读取: {read_independent}  |  多轮续写读取: {read_multi}")


def main():
    print("=" * 70)
    print("  Prompt Caching 测试")
    print(f"  API: {API_BASE}")
    print(f"  模型数量: {len(MODELS)}")
    print(f"  System Prompt 长度: ~{len(LONG_SYSTEM_PROMPT)} 字符")
    print("=" * 70)

    for model in MODELS:
        test_model(model)

    print(f"\n\n{'='*70}")
    print("  全部测试完成")
    print(f"{'='*70}")


if __name__ == "__main__":
    main()
