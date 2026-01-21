#!/usr/bin/env python3
"""
Kling Custom Elements 测试脚本

参考文档: https://app.klingai.com/cn/dev/document-api/apiReference/model/customElements

快速使用:
    1. 修改下面的 AK 和 SK
    2. 修改训练图片 URL
    3. 运行: python test_custom_elements.py
"""

import json
import time
import jwt
import requests
import argparse

# ============ 默认配置 ============
DEFAULT_AK = "AghpFynkahgeFtm3YRkQnK3Ageg4HkyC"  # 替换为你的 Access Key
DEFAULT_SK = "dT89J4JdHNpQbeEthpPTtRNABEADgNkn"  # 替换为你的 Secret Key

API_URL = "https://api-beijing.klingai.com/v1/general/custom-elements"

# 请求参数（根据官方文档）
DEFAULT_PARAMS = {
#                      "model": "kling-video-o1",
    "element_name": "自定义主体-001",
     "element_description": "自定义主体测试-001",
     "element_frontal_image": "https://docs.qingque.cn/image/api/convert/loadimage?id=-8654991330408162800eZQDlFDacBuEmer7HQstW4wes&docId=eZQAl5y8xNSkr0iYUS8-bpGvP&identityId=2Oa28mncRIC&loadSource=true",
     "element_refer_list": [
         {"image_url":"https://docs.qingque.cn/image/api/convert/loadimage?id=-8654991330408162800eZQDlFDacBuEmer7HQstW4wes&docId=eZQAl5y8xNSkr0iYUS8-bpGvP&identityId=2Oa28mncRIC&loadSource=true"}
     ],
     "tag_list": [
         {
             "tag_id": "o_101"
         }
     ]
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


def test_custom_elements(ak: str, sk: str, params: dict, api_url: str = API_URL):
    """测试 custom-elements 接口"""
    # 生成 token
    token = generate_jwt_token(ak, sk)

    # 发送请求
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json"
    }

    print(f"📤 发送请求到: {api_url}")
    print(f"📝 请求参数:")
    print(json.dumps(params, indent=2, ensure_ascii=False))
    print()

    try:
        response = requests.post(api_url, headers=headers, json=params, timeout=30)

        print(f"📥 响应状态: {response.status_code}")
        print(f"📄 响应内容:")

        result = response.json()
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # 判断结果
        message = result.get("message", "").upper()
        if message in ["SUCCESS", "SUCCEED"]:
            print("\n✅ 请求成功!")
            data = result.get("data", {})
            if data.get("task_id"):
                print(f"任务ID: {data['task_id']}")
                print(f"任务状态: {data.get('task_status', 'unknown')}")

            # custom-elements 是同步接口，可能直接返回结果
            if data.get("element_id"):
                print(f"元素ID: {data['element_id']}")
        else:
            print(f"\n❌ 请求失败: {result.get('message', 'Unknown error')}")
            if result.get("code"):
                print(f"错误代码: {result['code']}")

    except requests.exceptions.Timeout:
        print(f"\n❌ 请求超时")
    except requests.exceptions.RequestException as e:
        print(f"\n❌ 请求异常: {e}")
    except json.JSONDecodeError:
        print(f"\n❌ 响应解析失败，原始响应:")
        print(response.text)
    except Exception as e:
        print(f"\n❌ 未知异常: {e}")


def main():
    parser = argparse.ArgumentParser(description="测试 Kling Custom Elements API")
    parser.add_argument("--ak", default=DEFAULT_AK, help="Access Key")
    parser.add_argument("--sk", default=DEFAULT_SK, help="Secret Key")
    parser.add_argument("--element-name", help="元素名称")
    parser.add_argument("--element-description", help="元素描述")
    parser.add_argument("--frontal-image", help="正面图片 URL")
    parser.add_argument("--refer-images", nargs="+", help="参考图片 URL 列表")
    parser.add_argument("--tag-ids", nargs="+", help="标签 ID 列表")
    parser.add_argument("--url", default=API_URL, help="API URL")

    args = parser.parse_args()

    # 检查配置
    if args.ak == "your_access_key_here" or args.sk == "your_secret_key_here":
        print("❌ 错误: 请先配置 AK 和 SK!")
        print("\n方法1: 在脚本中修改 DEFAULT_AK 和 DEFAULT_SK")
        print("方法2: 使用命令行参数 --ak 和 --sk")
        return 1

    # 构建请求参数 - 使用命令行参数或默认值
    if args.element_name or args.frontal_image:
        # 如果提供了命令行参数，使用命令行配置
        params = {}

        if args.element_name:
            params["element_name"] = args.element_name

        if args.element_description:
            params["element_description"] = args.element_description

        if args.frontal_image:
            params["element_frontal_image"] = args.frontal_image

        if args.refer_images:
            params["element_refer_list"] = [{"image_url": url} for url in args.refer_images]

        if args.tag_ids:
            params["tag_list"] = [{"tag_id": tag_id} for tag_id in args.tag_ids]
    else:
        # 使用默认配置
        params = DEFAULT_PARAMS.copy()
        print("\n⚠️  使用默认配置参数")
        print("   可以通过以下参数自定义:")
        print("   --element-name 元素名称")
        print("   --element-description 元素描述")
        print("   --frontal-image 正面图片URL")
        print("   --refer-images 参考图片URL列表")
        print("   --tag-ids 标签ID列表\n")

    test_custom_elements(args.ak, args.sk, params, args.url)
    return 0



if __name__ == "__main__":
    exit(main())
