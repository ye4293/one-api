# Anthropic API 定向渠道缓存使用指南

## 概述

通过 `X-Response-ID` Header 实现定向渠道缓存寻找，可以在多次请求间复用缓存内容，降低 Token 消耗成本。

---

## 请求配置

### 接口地址

```
POST https://api.ezai88.com/v1/messages
```

### 请求 Headers

| Header 名称 | 值 | 说明 |
|------------|---|------|
| Content-Type | application/json | 请求体格式 |
| anthropic-version | 2023-06-01 | API 版本 |
| X-Api-Key | `<your-api-key>` | 认证密钥 |
| X-Response-ID | `<msg_id>` | **第二次请求时使用**，用于定向缓存寻找 |

---

## 使用流程

### 第一次请求：创建缓存

**请求体示例：**

以下示例展示了如何缓存《傲慢与偏见》的完整内容，以便进行多轮分析：

```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "system": [
    {
      "type": "text",
      "text": "# Major Themes in Pride and Prejudice\n\nJane Austen's *Pride and Prejudice* explores several interconnected themes that remain relevant today:\n\n## 1. **Pride and Prejudice (Misjudgment)**\nThe title itself highlights the central theme. Elizabeth's prejudice against Darcy based on first impressions, and Darcy's pride in his social position, create the main conflict. Both characters must overcome their flaws to find happiness. The novel suggests that self-awareness and humility are essential for personal growth and genuine relationships.\n\n## 2. **Class and Social Mobility**\nThe rigid class structure of Regency England permeates every relationship. Marriage is viewed as one of the few ways to improve (or maintain) social standing, particularly for women. The tension between the landed gentry (Darcy) and the genteel but less wealthy Bennets illustrates how class differences create obstacles to happiness.\n\n## 3. **Marriage and Economics**\nAusten presents various marriage models: Charlotte's pragmatic union with Mr. Collins, Lydia's reckless elopement, Jane and Bingley's romantic match, and Elizabeth and Darcy's partnership based on mutual respect and growth. The novel critiques marriages based solely on financial security while acknowledging economic realities women faced.\n\n## 4. **Women's Limited Options**\nThe entailment of the Bennet estate and Mrs. Bennet's anxiety about her daughters' futures highlight women's economic vulnerability. Without the ability to inherit property or pursue most professions, marriage was often a woman's only path to financial security.\n\n## 5. **Individual vs. Society**\nElizabeth's independence of mind sets her apart in a society that values conformity, particularly in women. Her refusal of Mr. Collins and initial rejection of Darcy demonstrate her determination to marry for love and respect rather than convenience.\n\nThese themes interweave throughout the novel, creating a nuanced social commentary wrapped in an engaging love story. IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.\n"
    },
    {
      "type": "text",
      "text": "<the entire contents of Pride and Prejudice>",
      "cache_control": {
        "type": "ephemeral"
      }
    }
  ],
  "messages": [
    {
      "role": "user",
      "content": "Analyze the major themes in Pride and Prejudice."
    }
  ]
}
```

**关键点：**
- `system` 数组中可包含多个内容块
- 第一个内容块：普通 system prompt（主题分析指南）
- 第二个内容块：需要缓存的大量内容（如完整书籍），添加 `cache_control` 字段
- `cache_control.type` 设置为 `ephemeral` 表示临时缓存

**响应示例：**

```json
{
  "id": "msg_0127WjurzfQMj2tW2iGLDutA",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-5-20250929",
  "content": [
    {
      "type": "text",
      "text": "# Major Themes in Pride and Prejudice\n\nJane Austen's *Pride and Prejudice* explores several interconnected themes that remain relevant today:\n\n## 1. **Pride and Prejudice (Misjudgment)**\nThe title itself highlights the central theme. Elizabeth's prejudice against Darcy based on first impressions, and Darcy's pride in his social position, create the main conflict. Both characters must overcome their flaws to find happiness. The novel suggests that self-awareness and humility are essential for personal growth and genuine relationships.\n\n## 2. **Class and Social Mobility**\nThe rigid class structure of Regency England permeates every relationship. Marriage is viewed as one of the few ways to improve (or maintain) social standing, particularly for women. The tension between the landed gentry (Darcy) and the genteel but less wealthy Bennets illustrates how class differences create obstacles to happiness.\n\n## 3. **Marriage and Economics**\nAusten presents various marriage models: Charlotte's pragmatic union with Mr. Collins, Lydia's reckless elopement, Jane and Bingley's romantic match, and Elizabeth and Darcy's partnership based on mutual respect and growth. The novel critiques marriages based solely on financial security while acknowledging economic realities women faced.\n\n## 4. **Women's Limited Options**\nThe entailment of the Bennet estate and Mrs. Bennet's anxiety about her daughters' futures highlight women's economic vulnerability. Without the ability to inherit property or pursue most professions, marriage was often a woman's only path to financial security.\n\n## 5. **Individual vs. Society**\nElizabeth's independence of mind sets her apart in a society that values conformity, particularly in women. Her refusal of Mr. Collins and initial rejection of Darcy demonstrate her determination to marry for love and respect rather than convenience.\n\nThese themes interweave throughout the novel, creating a nuanced social commentary wrapped in an engaging love story."
    }
  ],
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 18,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 1956,
    "output_tokens": 437,
    "cache_creation": {},
    "claude_cache_creation_5_m_tokens": 0,
    "claude_cache_creation_1_h_tokens": 0
  }
}
```

**重点：保存响应中的 `id` 字段值**（如：`msg_0127WjurzfQMj2tW2iGLDutA`）

---

### 第二次请求：使用缓存

在第二次请求时，添加 `X-Response-ID` Header 来定向寻找缓存：

**Headers：**

```
Content-Type: application/json
anthropic-version: 2023-06-01
X-Api-Key: <your-api-key>
X-Response-ID: msg_0127WjurzfQMj2tW2iGLDutA
```

**请求体示例（提问新问题）：**

```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "system": [
    {
      "type": "text",
      "text": "# Major Themes in Pride and Prejudice\n\nJane Austen's *Pride and Prejudice* explores several interconnected themes that remain relevant today:\n\n## 1. **Pride and Prejudice (Misjudgment)**\nThe title itself highlights the central theme. Elizabeth's prejudice against Darcy based on first impressions, and Darcy's pride in his social position, create the main conflict. Both characters must overcome their flaws to find happiness. The novel suggests that self-awareness and humility are essential for personal growth and genuine relationships.\n\n## 2. **Class and Social Mobility**\nThe rigid class structure of Regency England permeates every relationship. Marriage is viewed as one of the few ways to improve (or maintain) social standing, particularly for women. The tension between the landed gentry (Darcy) and the genteel but less wealthy Bennets illustrates how class differences create obstacles to happiness.\n\n## 3. **Marriage and Economics**\nAusten presents various marriage models: Charlotte's pragmatic union with Mr. Collins, Lydia's reckless elopement, Jane and Bingley's romantic match, and Elizabeth and Darcy's partnership based on mutual respect and growth. The novel critiques marriages based solely on financial security while acknowledging economic realities women faced.\n\n## 4. **Women's Limited Options**\nThe entailment of the Bennet estate and Mrs. Bennet's anxiety about her daughters' futures highlight women's economic vulnerability. Without the ability to inherit property or pursue most professions, marriage was often a woman's only path to financial security.\n\n## 5. **Individual vs. Society**\nElizabeth's independence of mind sets her apart in a society that values conformity, particularly in women. Her refusal of Mr. Collins and initial rejection of Darcy demonstrate her determination to marry for love and respect rather than convenience.\n\nThese themes interweave throughout the novel, creating a nuanced social commentary wrapped in an engaging love story. IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.IT is a truth universally acknowledged, that a single man in possession of a good fortune must be in want of a wife. However little known the feelings or views of such a man may be on his first entering a neighbourhood, this truth is so well fixed in the minds of the surrounding families, that he is considered as the rightful property of some one or other of their daughters.\n\nMy dear Mr. Bennet, said his lady to him one day, have you heard that Netherfield Park is let at last?\n\nMr. Bennet replied that he had not.\n\nBut it is, returned she; for Mrs. Long has just been here, and she told me all about it.\n\nMr. Bennet made no answer.\n\nDo not you want to know who has taken it? cried his wife impatiently.\n\nYou want to tell me, and I have no objection to hearing it.\n\nThis was invitation enough.\n\nWhy, my dear, you must know, Mrs. Long says that Netherfield is taken by a young man of large fortune from the north of England; that he came down on Monday in a chaise and four to see the place, and was so much delighted with it that he agreed with Mr. Morris immediately; that he is to take possession before Michaelmas, and some of his servants are to be in the house by the end of next week.\n\nWhat is his name?\n\nBingley.\n\nIs he married or single?\n\nOh! single, my dear, to be sure! A single man of large fortune; four or five thousand a year. What a fine thing for our girls!\n\nHow so? how can it affect them?\n\nMy dear Mr. Bennet, replied his wife, how can you be so tiresome! You must know that I am thinking of his marrying one of them.\n\nIs that his design in settling here?\n\nDesign! nonsense, how can you talk so! But it is very likely that he may fall in love with one of them, and therefore you must visit him as soon as he comes.\n\nI see no occasion for that. You and the girls may go, or you may send them by themselves, which perhaps will be still better; for, as you are as handsome as any of them, Mr. Bingley might like you the best of the party.\n"
    },
    {
      "type": "text",
      "text": "<the entire contents of Pride and Prejudice>",
      "cache_control": {
        "type": "ephemeral"
      }
    }
  ],
  "messages": [
    {
      "role": "user",
      "content": "Analyze the major themes in Pride and Prejudice."
    }
  ]
}
```

**说明：**
- `system` 内容保持与第一次请求完全一致
- `messages` 中可以提问新的问题
- 通过 `X-Response-ID` 定向到之前的缓存渠道，实现缓存复用

---

## Usage 字段说明

| 字段 | 说明 |
|-----|------|
| `input_tokens` | 实际输入的 token 数量（不含缓存部分） |
| `cache_creation_input_tokens` | 本次创建缓存的 token 数量 |
| `cache_read_input_tokens` | 从缓存读取的 token 数量 |
| `output_tokens` | 输出的 token 数量 |
| `claude_cache_creation_5_m_tokens` | 5 分钟有效期缓存创建的 token 数量 |
| `claude_cache_creation_1_h_tokens` | 1 小时有效期缓存创建的 token 数量 |

**示例解读：**

```json
"usage": {
  "input_tokens": 18,
  "cache_creation_input_tokens": 0,
  "cache_read_input_tokens": 1956,
  "output_tokens": 437
}
```

- `input_tokens: 18` - 用户消息等非缓存内容的 token
- `cache_read_input_tokens: 1956` - 从缓存读取了 1956 个 token（缓存命中）
- `cache_creation_input_tokens: 0` - 未创建新缓存（使用了已有缓存）

**缓存命中时**，`cache_read_input_tokens` 会有值，表示节省了这些 token 的处理成本。

---

## 注意事项

1. **缓存有效期**：`ephemeral` 类型的缓存为临时缓存，有一定时效性
2. **内容一致性**：第二次请求的 `system` 缓存内容需要与第一次请求**完全一致**才能命中缓存
3. **X-Response-ID**：必须使用上一次响应返回的 `id` 值
4. **渠道定向**：通过 X-Response-ID 可以确保请求被路由到相同的缓存渠道
5. **适用场景**：适合需要多轮对话分析同一份大型文档（如书籍、代码库、长文档）的场景
