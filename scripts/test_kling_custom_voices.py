#!/usr/bin/env python3
"""
Kling API Custom Voices 测试脚本

使用方法:
    python test_kling_custom_voices.py

配置 AK/SK:
    方式1: 命令行参数
        python test_kling_custom_voices.py --ak YOUR_AK --sk YOUR_SK

    方式2: 环境变量
        export KLING_AK="your_access_key"
        export KLING_SK="your_secret_key"
        python test_kling_custom_voices.py

    方式3: 修改脚本中的默认值
"""

import json
import time
import argparse
import os

try:
    import jwt
    import requests
except ImportError:
    print("缺少依赖库，请先安装:")
    print("  pip install pyjwt requests")
    exit(1)


class KlingAPIClient:
    """Kling API 客户端"""

    def __init__(self, ak: str, sk: str, base_url: str = "https://api-beijing.klingai.com"):
        self.ak = ak
        self.sk = sk
        self.base_url = base_url.rstrip('/')

    def generate_jwt_token(self) -> str:
        """生成 JWT Token (官方文档方法)"""
        headers = {
            "alg": "HS256",
            "typ": "JWT"
        }
        payload = {
            "iss": self.ak,  # issuer: Access Key
            "exp": int(time.time()) + 1800,  # 有效时间，当前时间+1800s(30min)
            "nbf": int(time.time()) - 5       # 开始生效的时间，当前时间-5秒
        }
        token = jwt.encode(payload, self.sk, headers=headers)
        return token

    def custom_voices(self, model: str, voice_name: str, voice_url: str,
                     callback_url: str = None, external_task_id: str = None) -> dict:
        """
        调用 custom-voices 接口

        Args:
            model: 模型名称，如 "kling-video-o1"
            voice_name: 自定义声音名称
            voice_url: 声音文件URL
            callback_url: 可选的回调URL
            external_task_id: 可选的外部任务ID

        Returns:
            API 响应的 JSON 数据
        """
        # 生成 JWT Token
        token = self.generate_jwt_token()

        # 构建请求
        url = f"{self.base_url}/v1/general/custom-voices"

        headers = {
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        }

        payload = {
            "model": model,
            "voice_name": voice_name,
            "voice_url": voice_url
        }

        # 添加可选参数
        if callback_url:
            payload["callback_url"] = callback_url
        if external_task_id:
            payload["external_task_id"] = external_task_id

        # 打印请求信息
        print(f"\n{'='*60}")
        print(f"请求 URL: {url}")
        print(f"请求头: Authorization: Bearer {token[:20]}...")
        print(f"请求体:")
        print(json.dumps(payload, indent=2, ensure_ascii=False))
        print(f"{'='*60}\n")

        # 发送请求
        try:
            response = requests.post(url, headers=headers, json=payload, timeout=30)

            # 打印响应信息
            print(f"响应状态码: {response.status_code}")
            print(f"响应头: {dict(response.headers)}")
            print(f"\n响应体:")

            try:
                response_json = response.json()
                print(json.dumps(response_json, indent=2, ensure_ascii=False))
                return response_json
            except json.JSONDecodeError:
                print(f"无法解析 JSON，原始响应:")
                print(response.text)
                return {"error": "Invalid JSON response", "raw": response.text}

        except requests.exceptions.RequestException as e:
            print(f"请求失败: {e}")
            return {"error": str(e)}


def main():
    """主函数"""
    parser = argparse.ArgumentParser(
        description="Kling API Custom Voices 测试脚本",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  # 使用命令行参数
  python %(prog)s --ak YOUR_AK --sk YOUR_SK

  # 使用环境变量
  export KLING_AK="your_ak"
  export KLING_SK="your_sk"
  python %(prog)s

  # 自定义参数
  python %(prog)s --ak YOUR_AK --sk YOUR_SK --model kling-video-o1 --voice-name "测试声音"
        """
    )

    # AK/SK 配置
    parser.add_argument("--ak", help="Access Key (或使用环境变量 KLING_AK)")
    parser.add_argument("--sk", help="Secret Key (或使用环境变量 KLING_SK)")

    # API 参数
    parser.add_argument("--base-url", default="https://api-beijing.klingai.com",
                       help="API Base URL (默认: https://api-beijing.klingai.com)")
    parser.add_argument("--model", default="kling-video-o1",
                       help="模型名称 (默认: kling-video-o1)")
    parser.add_argument("--voice-name", default="自定义主体-001",
                       help="自定义声音名称 (默认: 自定义主体-001)")
    parser.add_argument("--voice-url",
                       default="https://sis-sample-audio.obs.cn-north-1.myhuaweicloud.com/16k16bit.mp3",
                       help="声音文件URL")
    parser.add_argument("--callback-url", help="可选的回调URL")
    parser.add_argument("--external-task-id", help="可选的外部任务ID")

    args = parser.parse_args()

    # 获取 AK/SK (优先级: 命令行参数 > 环境变量)
    ak = args.ak or os.getenv("KLING_AK")
    sk = args.sk or os.getenv("KLING_SK")

    if not ak or not sk:
        print("错误: 未提供 AK/SK!")
        print("\n请通过以下方式之一提供:")
        print("  1. 命令行参数: --ak YOUR_AK --sk YOUR_SK")
        print("  2. 环境变量: export KLING_AK=... KLING_SK=...")
        print("\n使用 --help 查看更多帮助")
        exit(1)

    # 创建客户端
    client = KlingAPIClient(ak=ak, sk=sk, base_url=args.base_url)

    # 调用 API
    print(f"\n🚀 开始调用 Kling Custom Voices API...")
    print(f"AK: {ak[:10]}***")
    print(f"SK: {sk[:10]}***")

    result = client.custom_voices(
        model=args.model,
        voice_name=args.voice_name,
        voice_url=args.voice_url,
        callback_url=args.callback_url,
        external_task_id=args.external_task_id
    )

    # 分析结果
    print(f"\n{'='*60}")
    if "error" in result:
        print("❌ 请求失败")
    else:
        message = result.get("message", "").upper()
        if message in ["SUCCESS", "SUCCEED"]:
            print("✅ 请求成功")
            if "data" in result and "task_id" in result["data"]:
                print(f"任务ID: {result['data']['task_id']}")
                print(f"任务状态: {result['data'].get('task_status', 'unknown')}")
        else:
            print(f"⚠️  请求返回非成功消息: {result.get('message', 'Unknown')}")
    print(f"{'='*60}\n")


if __name__ == "__main__":
    main()
