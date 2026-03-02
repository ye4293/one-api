import json

with open("可用模型.txt", "r", encoding="utf-8") as f:
    data = json.load(f)

gpt_models = []
claude_models = []
gemini_models = []
other_models = []

for model in data["data"]:
    mid = model["id"]
    if mid.startswith(("gpt-", "o1", "o3", "o4", "chatgpt-", "dall-e", "net-o1")):
        gpt_models.append(mid)
    elif mid.startswith("claude-"):
        claude_models.append(mid)
    elif mid.startswith("gemini-"):
        gemini_models.append(mid)
    else:
        other_models.append(mid)

gpt_models.sort()
claude_models.sort()
gemini_models.sort()
other_models.sort()

def print_section(title, models):
    print(f"\n{'='*60}")
    print(f" {title} ({len(models)} 个)")
    print(f"{'='*60}")
    for m in models:
        print(f"  {m}")

print_section("OpenAI 系列 (gpt/o1/o3/o4/chatgpt/dall-e)", gpt_models)
print_section("Claude 系列 (claude-x)", claude_models)
print_section("Gemini 系列 (gemini-x)", gemini_models)
print_section("其他模型", other_models)

with open("模型分类结果.txt", "w", encoding="utf-8") as f:
    def write_section(title, models):
        f.write(f"\n{'='*60}\n")
        f.write(f" {title} ({len(models)} 个)\n")
        f.write(f"{'='*60}\n")
        for m in models:
            f.write(f"  {m}\n")

    write_section("OpenAI 系列 (gpt/o1/o3/o4/chatgpt/dall-e)", gpt_models)
    write_section("Claude 系列 (claude-x)", claude_models)
    write_section("Gemini 系列 (gemini-x)", gemini_models)
    write_section("其他模型", other_models)

print(f"\n结果已保存到 模型分类结果.txt")
