#!/usr/bin/env python3
"""
Kling Custom Voices 简化测试脚本

快速使用:
    1. 修改下面的 AK 和 SK
    2. 运行: python test_kling_simple.py
"""

import json
import time
import jwt
import requests

# ============ 配置区域 ============
AK = "AghpFynkahgeFtm3YRkQnK3Ageg4HkyC"  # 替换为你的 Access Key
SK = "dT89J4JdHNpQbeEthpPTtRNABEADgNkn"  # 替换为你的 Secret Key

API_URL = "https://api-beijing.klingai.com/v1/general/custom-voices"

# 请求参数
PARAMS = {
    "model": "kling-video-o1",
    "voice_name": "自定义主体-001",
    "voice_url": "https://sis-sample-audio.obs.cn-north-1.myhuaweicloud.com/16k16bit.mp3"
}
# ================================


def generate_jwt_token(ak: str, sk: str) -> str:
    """生成 JWT Token (官方文档方法)"""
    headers = {
        "alg": "HS256",
        "typ": "JWT"
    }
    payload = {
        "iss": ak,
        "exp": int(time.time()) + 1800,  # 有效时间，当前时间+1800s(30min)
        "nbf": int(time.time()) - 5       # 开始生效的时间，当前时间-5秒
    }
    token = jwt.encode(payload, sk, headers=headers)
    return token


def test_custom_voices():
    """测试 custom-voices 接口"""
    # 生成 token
    token = generate_jwt_token(AK, SK)

    # 发送请求
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json"
    }

    print(f"📤 发送请求到: {API_URL}")
    print(f"📝 请求参数:")
    print(json.dumps(PARAMS, indent=2, ensure_ascii=False))
    print()

    try:
        response = requests.post(API_URL, headers=headers, json=PARAMS, timeout=30)

        print(f"📥 响应状态: {response.status_code}")
        print(f"📄 响应内容:")

        result = response.json()
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # 判断结果
        message = result.get("message", "").upper()
        if message in ["SUCCESS", "SUCCEED"]:
            print("\n✅ 请求成功!")
            if result.get("data", {}).get("task_id"):
                print(f"任务ID: {result['data']['task_id']}")
                print(f"任务状态: {result['data'].get('task_status', 'unknown')}")
        else:
            print(f"\n❌ 请求失败: {result.get('message', 'Unknown error')}")

    except Exception as e:
        print(f"\n❌ 请求异常: {e}")


if __name__ == "__main__":
    # 检查配置
    if AK == "your_access_key_here" or SK == "your_secret_key_here":
        print("❌ 错误: 请先在脚本中配置 AK 和 SK!")
        print("\n打开脚本文件，修改以下行:")
        print("  AK = \"your_access_key_here\"")
        print("  SK = \"your_secret_key_here\"")
        exit(1)

    test_custom_voices()
