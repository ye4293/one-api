"""
测试 Mid-conversation system messages 功能是否被当前 API 支持。

测试方法：
1. 基线请求 - 正常的 user/assistant/user 对话（确认 API 可用）
2. Mid-conv system 请求 - 在 user turn 之后插入 role: "system" 消息
3. 对比两者的响应状态码和内容
"""

import requests
import json
import sys

BASE_URL = "https://api.ezlinkai.com"
API_KEY = "dfoc9bnUFJZGf1Z57fC2D4Af93864e0f93890f3f5458E7Ec"

HEADERS = {
    "Content-Type": "application/json",
    "Authorization": f"Bearer {API_KEY}",
}

MODEL = "claude-opus-4-8"


def test_baseline():
    """测试 1: 基线 - 普通多轮对话"""
    print("=" * 60)
    print("测试 1: 基线请求（普通多轮对话）")
    print("=" * 60)

    payload = {
        "model": MODEL,
        "max_tokens": 100,
        "messages": [
            {"role": "user", "content": "说'你好'"},
        ],
    }

    resp = requests.post(f"{BASE_URL}/v1/messages", headers=HEADERS, json=payload)
    print(f"状态码: {resp.status_code}")
    if resp.status_code == 200:
        data = resp.json()
        content = data.get("content", [{}])[0].get("text", "")
        print(f"模型响应: {content[:100]}")
        print("✅ 基线请求成功")
    else:
        print(f"❌ 基线请求失败: {resp.text[:300]}")
    print()
    return resp.status_code


def test_mid_conversation_system_message():
    """测试 2: Mid-conversation system message（在 user turn 之后插入 system 消息）"""
    print("=" * 60)
    print("测试 2: Mid-conversation system message")
    print("=" * 60)

    payload = {
        "model": MODEL,
        "max_tokens": 100,
        "messages": [
            {"role": "user", "content": "你好，请记住我的名字叫小明"},
            {"role": "assistant", "content": "你好小明！我记住了。"},
            {"role": "user", "content": "接下来请按照新的指令回答"},
            {"role": "system", "content": "从现在开始，你必须用英文回答所有问题。"},
            {"role": "user", "content": "我叫什么名字？"},
        ],
    }

    resp = requests.post(f"{BASE_URL}/v1/messages", headers=HEADERS, json=payload)
    print(f"状态码: {resp.status_code}")
    if resp.status_code == 200:
        data = resp.json()
        content = data.get("content", [{}])[0].get("text", "")
        print(f"模型响应: {content[:200]}")
        print("✅ Mid-conversation system message 被接受！功能可用。")
    elif resp.status_code == 400:
        error = resp.json().get("error", {})
        msg = error.get("message", resp.text[:300])
        print(f"❌ 400 错误: {msg}")
        print("⚠️  该模型/端点不支持 mid-conversation system messages")
    else:
        print(f"❌ 非预期错误 ({resp.status_code}): {resp.text[:300]}")
    print()
    return resp.status_code


def test_mid_conv_wrong_placement():
    """测试 3: 错误放置 - system 在 assistant turn 之后（应该失败）"""
    print("=" * 60)
    print("测试 3: 错误放置（system 紧跟 assistant turn，应被拒绝）")
    print("=" * 60)

    payload = {
        "model": MODEL,
        "max_tokens": 100,
        "messages": [
            {"role": "user", "content": "你好"},
            {"role": "assistant", "content": "你好！"},
            {"role": "system", "content": "这条 system 放在 assistant 之后，应该违反 placement rules。"},
            {"role": "user", "content": "测试"},
        ],
    }

    resp = requests.post(f"{BASE_URL}/v1/messages", headers=HEADERS, json=payload)
    print(f"状态码: {resp.status_code}")
    if resp.status_code == 400:
        error = resp.json().get("error", {})
        msg = error.get("message", resp.text[:300])
        print(f"返回错误: {msg}")
        print("✅ 正确拒绝了错误放置的 system message（符合 placement rules）")
    elif resp.status_code == 200:
        data = resp.json()
        content = data.get("content", [{}])[0].get("text", "")
        print(f"模型响应: {content[:100]}")
        print("⚠️  错误放置也被接受了（可能端点未校验 placement rules）")
    else:
        print(f"返回: {resp.text[:300]}")
    print()
    return resp.status_code


def test_with_top_system_and_mid_system():
    """测试 4: 同时使用顶层 system 和 mid-conversation system"""
    print("=" * 60)
    print("测试 4: 顶层 system + mid-conversation system 共存")
    print("=" * 60)

    payload = {
        "model": MODEL,
        "max_tokens": 100,
        "system": "你是一个友好的助手，始终用中文回答。",
        "messages": [
            {"role": "user", "content": "你好"},
            {"role": "assistant", "content": "你好！有什么可以帮你的？"},
            {"role": "user", "content": "好的，注意新指令"},
            {"role": "system", "content": "更新指令：从现在开始每句话末尾加上 [已更新]"},
            {"role": "user", "content": "现在几点了？"},
        ],
    }

    resp = requests.post(f"{BASE_URL}/v1/messages", headers=HEADERS, json=payload)
    print(f"状态码: {resp.status_code}")
    if resp.status_code == 200:
        data = resp.json()
        content = data.get("content", [{}])[0].get("text", "")
        print(f"模型响应: {content[:200]}")
        print("✅ 顶层 system + mid-conv system 共存可用")
    else:
        error_text = resp.text[:300]
        print(f"❌ 失败: {error_text}")
    print()
    return resp.status_code


if __name__ == "__main__":
    print("\n🔍 Mid-conversation System Messages 功能测试")
    print(f"   端点: {BASE_URL}")
    print(f"   模型: {MODEL}")
    print()

    results = {}

    results["baseline"] = test_baseline()

    if results["baseline"] != 200:
        print("⛔ 基线请求失败，请检查 API key / 模型名称 / 网络连通性")
        print("   尝试更换模型...")
        # 回退测试其他模型
        for fallback in ["claude-sonnet-4-6", "claude-opus-4-7", "claude-sonnet-4-5-20241022"]:
            MODEL = fallback
            print(f"\n   尝试模型: {MODEL}")
            payload = {
                "model": MODEL,
                "max_tokens": 50,
                "messages": [{"role": "user", "content": "hi"}],
            }
            r = requests.post(f"{BASE_URL}/v1/messages", headers=HEADERS, json=payload)
            if r.status_code == 200:
                print(f"   ✅ {MODEL} 可用，使用此模型继续测试")
                results["baseline"] = 200
                break
            else:
                print(f"   ❌ {MODEL} 不可用: {r.status_code}")
        else:
            print("\n⛔ 所有模型都不可用，终止测试")
            sys.exit(1)

    results["mid_conv"] = test_mid_conversation_system_message()
    results["wrong_placement"] = test_mid_conv_wrong_placement()
    results["combined"] = test_with_top_system_and_mid_system()

    # 汇总
    print("=" * 60)
    print("📊 测试汇总")
    print("=" * 60)
    print(f"  基线请求:              {'✅ 通过' if results['baseline'] == 200 else '❌ 失败'}")
    print(f"  Mid-conv system msg:   {'✅ 支持' if results['mid_conv'] == 200 else '❌ 不支持'}")
    print(f"  错误放置校验:          {'✅ 正确拒绝' if results['wrong_placement'] == 400 else '⚠️ 未按预期'}")
    print(f"  顶层+Mid-conv共存:     {'✅ 支持' if results['combined'] == 200 else '❌ 不支持'}")
    print()

    if results["mid_conv"] == 200:
        print("🎉 结论: 当前端点 + 模型 支持 Mid-conversation system messages")
    else:
        print("📌 结论: 当前端点 + 模型 不支持 Mid-conversation system messages")
        print("   可能原因:")
        print("   1. 模型版本不支持（需要 claude-opus-4-8）")
        print("   2. API 代理层（one-api）未透传 mid-conv system 消息")
        print("   3. 上游渠道不支持此特性")
