#!/usr/bin/env python3
"""
批量测试 AWS Bedrock AK/SK 是否能访问指定模型。

用法:
    # 从文件读取（每行一个 key，格式: ak|sk|region）
    python3 test_bedrock_key.py -f keys.txt

    # 直接传入多个 key
    python3 test_bedrock_key.py "AK|SK|us-east-1" "AK|SK|us-west-2"

    # 指定模型
    python3 test_bedrock_key.py -f keys.txt --model anthropic.claude-sonnet-4-20250514-v1:0

key 格式:  access_key|secret_key|region
"""

import sys
import json
import hashlib
import hmac
import urllib.request
import urllib.error
from datetime import datetime, timezone


def sign(key: bytes, msg: str) -> bytes:
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def get_signature_key(secret_key: str, date_stamp: str, region: str, service: str) -> bytes:
    k_date = sign(("AWS4" + secret_key).encode("utf-8"), date_stamp)
    k_region = sign(k_date, region)
    k_service = sign(k_region, service)
    k_signing = sign(k_service, "aws4_request")
    return k_signing


def invoke_model(url: str, headers: dict, payload: bytes) -> dict:
    req = urllib.request.Request(url, data=payload, method="POST")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = json.loads(resp.read())
            return {"success": True, "response": body}
    except urllib.error.HTTPError as e:
        error_body = e.read().decode("utf-8", errors="replace")
        return {"success": False, "status": e.code, "error": error_body}
    except Exception as e:
        return {"success": False, "error": str(e)}


def test_sigv4(access_key: str, secret_key: str, region: str, model_id: str) -> dict:
    service = "bedrock"
    host = f"bedrock-runtime.{region}.amazonaws.com"
    url = f"https://{host}/model/{model_id}/invoke"
    canonical_uri = f"/model/{model_id}/invoke"

    payload = json.dumps({
        "anthropic_version": "bedrock-2023-05-31",
        "max_tokens": 10,
        "messages": [{"role": "user", "content": "Hi"}]
    }).encode("utf-8")

    now = datetime.now(timezone.utc)
    amz_date = now.strftime("%Y%m%dT%H%M%SZ")
    date_stamp = now.strftime("%Y%m%d")
    payload_hash = hashlib.sha256(payload).hexdigest()

    canonical_headers = f"content-type:application/json\nhost:{host}\nx-amz-date:{amz_date}\n"
    signed_headers = "content-type;host;x-amz-date"
    canonical_request = f"POST\n{canonical_uri}\n\n{canonical_headers}\n{signed_headers}\n{payload_hash}"

    credential_scope = f"{date_stamp}/{region}/{service}/aws4_request"
    string_to_sign = (
        f"AWS4-HMAC-SHA256\n{amz_date}\n{credential_scope}\n"
        f"{hashlib.sha256(canonical_request.encode('utf-8')).hexdigest()}"
    )

    signing_key = get_signature_key(secret_key, date_stamp, region, service)
    signature = hmac.new(signing_key, string_to_sign.encode("utf-8"), hashlib.sha256).hexdigest()

    headers = {
        "Content-Type": "application/json",
        "X-Amz-Date": amz_date,
        "Authorization": (
            f"AWS4-HMAC-SHA256 Credential={access_key}/{credential_scope}, "
            f"SignedHeaders={signed_headers}, Signature={signature}"
        ),
    }
    return invoke_model(url, headers, payload)


def parse_and_test(key_line: str, model_id: str) -> dict:
    key_line = key_line.strip()
    if not key_line or key_line.startswith("#"):
        return {"ak": "-", "region": "-", "status": "skip", "detail": "空行或注释"}

    parts = key_line.split("|")
    if len(parts) != 3:
        print(f"  跳过: 格式错误（{len(parts)} 段，需要 ak|sk|region 三段）")
        return {"ak": key_line[:20], "region": "-", "status": "skip", "detail": f"格式错误({len(parts)}段)"}

    access_key, secret_key, region = parts[0].strip(), parts[1].strip(), parts[2].strip()
    ak_short = f"{access_key[:8]}...{access_key[-4:]}" if len(access_key) > 12 else access_key

    print(f"  区域: {region}")
    print(f"  AK:   {ak_short}")

    result = test_sigv4(access_key, secret_key, region, model_id)

    if result["success"]:
        resp = result.get("response", {})
        content = ""
        if "content" in resp and resp["content"]:
            content = resp["content"][0].get("text", "")
        print(f"  结果: ✅ 成功")
        if content:
            print(f"  回复: {content[:80]}")
        usage = resp.get("usage", {})
        if usage:
            print(f"  用量: input={usage.get('input_tokens', '?')}, output={usage.get('output_tokens', '?')}")
        return {"ak": ak_short, "region": region, "status": "ok", "detail": "可访问"}
    else:
        status = result.get("status", "N/A")
        error_raw = result.get("error", "未知错误")
        try:
            error_obj = json.loads(error_raw)
            msg = error_obj.get("message", error_raw)
        except (json.JSONDecodeError, TypeError):
            msg = str(error_raw)[:200]
        print(f"  结果: ❌ HTTP {status}")
        print(f"  错误: {msg}")
        hints = {403: "无权限", 404: "模型不存在/区域不支持", 400: "模型未开通", 401: "认证失败"}
        return {"ak": ak_short, "region": region, "status": "fail", "detail": hints.get(status, f"HTTP {status}")}


def main():
    args = sys.argv[1:]
    if not args:
        print(__doc__)
        sys.exit(1)

    model_id = "global.anthropic.claude-opus-4-7"
    key_lines = []

    i = 0
    while i < len(args):
        if args[i] == "--model" and i + 1 < len(args):
            model_id = args[i + 1]
            i += 2
        elif args[i] == "-f" and i + 1 < len(args):
            with open(args[i + 1], "r") as f:
                key_lines.extend(f.readlines())
            i += 2
        else:
            key_lines.append(args[i])
            i += 1

    key_lines = [l.strip() for l in key_lines if l.strip() and not l.strip().startswith("#")]
    if not key_lines:
        print("错误: 未提供任何 key")
        sys.exit(1)

    print(f"模型: {model_id}")
    print(f"共 {len(key_lines)} 个 key 待测试")
    print("=" * 60)

    results = []
    for idx, line in enumerate(key_lines, 1):
        ak_preview = line.split("|")[0][:16] + "..." if "|" in line else line[:20]
        region_preview = line.split("|")[-1].strip() if "|" in line else "?"
        print(f"\n[{idx}/{len(key_lines)}] AK={ak_preview}  Region={region_preview}")
        results.append(parse_and_test(line, model_id))

    ok_list = [r for r in results if r["status"] == "ok"]
    fail_list = [r for r in results if r["status"] == "fail"]
    skip_list = [r for r in results if r["status"] == "skip"]

    print("\n" + "=" * 60)
    print(f"  测试汇总  模型: {model_id}")
    print("=" * 60)
    print(f"  总计: {len(results)}  |  ✅ 可用: {len(ok_list)}  |  ❌ 不可用: {len(fail_list)}  |  ⏭ 跳过: {len(skip_list)}")
    print("-" * 60)
    print(f"  {'#':<4} {'状态':<4} {'AK':<22} {'区域':<15} {'说明'}")
    print(f"  {'--':<4} {'--':<4} {'--':<22} {'--':<15} {'--'}")
    for idx, r in enumerate(results, 1):
        icon = {"ok": "✅", "fail": "❌", "skip": "⏭"}[r["status"]]
        print(f"  {idx:<4} {icon:<4} {r['ak']:<22} {r['region']:<15} {r['detail']}")

    if ok_list:
        print(f"\n✅ 可用 ({len(ok_list)}):")
        for r in ok_list:
            print(f"  {r['ak']}  {r['region']}")
    if fail_list:
        print(f"\n❌ 不可用 ({len(fail_list)}):")
        for r in fail_list:
            print(f"  {r['ak']}  {r['region']}  - {r['detail']}")

    print("\n" + "=" * 60)


if __name__ == "__main__":
    main()
