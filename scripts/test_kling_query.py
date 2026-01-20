#!/usr/bin/env python3
"""
Kling 查询接口测试脚本

测试新增的透明代理查询接口：
- GET /v1/general/custom-elements (查询自定义元素列表)
- GET /v1/general/presets-elements (查询预设元素列表)
- GET /v1/general/custom-voices (查询自定义声音列表)
- GET /v1/general/presets-voices (查询预设声音列表)
- GET /v1/general/custom-voices/{id} (查询指定声音)
- DELETE /v1/general/delete-elements (删除元素)
- DELETE /v1/general/delete-voices (删除声音)

使用方法:
    python test_kling_query.py --endpoint custom-elements
    python test_kling_query.py --endpoint presets-elements
    python test_kling_query.py --endpoint custom-voices
    python test_kling_query.py --endpoint custom-voices --voice-id 123456
"""

import json
import argparse
import requests

# ============ 默认配置 ============
ONE_API_BASE_URL = "http://localhost:3000"
ONE_API_TOKEN = "sk-xxx"  # 替换为你的 One API Token

# 接口映射
ENDPOINTS = {
    "custom-elements": {
        "method": "GET",
        "path": "/kling/v1/general/custom-elements",
        "description": "查询自定义元素列表"
    },
    "presets-elements": {
        "method": "GET",
        "path": "/kling/v1/general/presets-elements",
        "description": "查询预设元素列表"
    },
    "custom-voices": {
        "method": "GET",
        "path": "/kling/v1/general/custom-voices",
        "description": "查询自定义声音列表"
    },
    "presets-voices": {
        "method": "GET",
        "path": "/kling/v1/general/presets-voices",
        "description": "查询预设声音列表"
    },
    "delete-elements": {
        "method": "DELETE",
        "path": "/kling/v1/general/delete-elements",
        "description": "删除元素"
    },
    "delete-voices": {
        "method": "DELETE",
        "path": "/kling/v1/general/delete-voices",
        "description": "删除声音"
    }
}


def test_query_endpoint(endpoint_key, voice_id=None, params=None, body=None):
    """测试查询接口"""
    if endpoint_key not in ENDPOINTS:
        print(f"❌ 未知的接口: {endpoint_key}")
        print(f"可用接口: {', '.join(ENDPOINTS.keys())}")
        return 1

    endpoint = ENDPOINTS[endpoint_key]
    method = endpoint["method"]
    path = endpoint["path"]
    description = endpoint["description"]

    # 构建完整 URL
    if voice_id and endpoint_key == "custom-voices":
        url = f"{ONE_API_BASE_URL}{path}/{voice_id}"
    else:
        url = f"{ONE_API_BASE_URL}{path}"

    # 添加查询参数
    if params:
        url += "?" + "&".join([f"{k}={v}" for k, v in params.items()])

    # 设置请求头
    headers = {
        "Authorization": f"Bearer {ONE_API_TOKEN}",
        "Content-Type": "application/json"
    }

    print(f"📤 测试接口: {description}")
    print(f"   方法: {method}")
    print(f"   URL: {url}")
    if body:
        print(f"   请求体: {json.dumps(body, indent=2, ensure_ascii=False)}")
    print()

    try:
        # 发送请求
        if method == "GET":
            response = requests.get(url, headers=headers, timeout=30)
        elif method == "DELETE":
            response = requests.delete(url, headers=headers, json=body, timeout=30)
        else:
            print(f"❌ 不支持的方法: {method}")
            return 1

        print(f"📥 响应状态: {response.status_code}")
        print(f"📄 响应内容:")

        result = response.json()
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # 判断结果
        if response.status_code == 200:
            code = result.get("code", -1)
            if code == 0:
                print(f"\n✅ 请求成功!")
                data = result.get("data", {})
                print(f"数据: {json.dumps(data, indent=2, ensure_ascii=False)}")
            else:
                print(f"\n⚠️  API 返回错误: code={code}, message={result.get('message', 'unknown')}")
        else:
            print(f"\n❌ 请求失败: HTTP {response.status_code}")

    except requests.exceptions.Timeout:
        print(f"\n❌ 请求超时")
        return 1
    except requests.exceptions.RequestException as e:
        print(f"\n❌ 请求异常: {e}")
        return 1
    except json.JSONDecodeError:
        print(f"\n❌ 响应解析失败，原始响应:")
        print(response.text)
        return 1
    except Exception as e:
        print(f"\n❌ 未知异常: {e}")
        return 1

    return 0


def main():
    parser = argparse.ArgumentParser(description="测试 Kling 查询接口")
    parser.add_argument("--endpoint", required=True, choices=list(ENDPOINTS.keys()),
                        help="要测试的接口")
    parser.add_argument("--voice-id", help="声音 ID（用于查询指定声音）")
    parser.add_argument("--element-id", help="元素 ID（用于删除元素）")
    parser.add_argument("--base-url", default=ONE_API_BASE_URL, help="One API 基础 URL")
    parser.add_argument("--token", default=ONE_API_TOKEN, help="One API Token")

    args = parser.parse_args()

    # 更新全局配置
    global ONE_API_BASE_URL, ONE_API_TOKEN
    ONE_API_BASE_URL = args.base_url
    ONE_API_TOKEN = args.token

    # 检查 Token
    if ONE_API_TOKEN == "sk-xxx":
        print("❌ 错误: 请先配置 ONE_API_TOKEN!")
        print("\n方法1: 在脚本中修改 ONE_API_TOKEN")
        print("方法2: 使用命令行参数 --token")
        return 1

    # 构建请求参数
    params = None
    body = None

    # 删除接口需要请求体
    if args.endpoint == "delete-elements" and args.element_id:
        body = {"element_id": args.element_id}
    elif args.endpoint == "delete-voices" and args.voice_id:
        body = {"voice_id": args.voice_id}

    return test_query_endpoint(args.endpoint, args.voice_id, params, body)


if __name__ == "__main__":
    exit(main())
