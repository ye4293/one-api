---
allowed-tools: Bash(git add:*), Bash(git status:*), Bash(git commit:*), Bash(git diff:*), Bash(git log:*), Bash(grep:*), Bash(cat:*), Read, Edit
description: 创建 git commit 并确保 CHANGELOG 已更新
---

## Context

- Current git status: !`git status`
- Current git diff (staged and unstaged changes): !`git diff HEAD`
- Current branch: !`git branch --show-current`
- Recent commits: !`git log --oneline -10`

## CHANGELOG 检查（强制）

在创建 commit 之前，**必须**执行以下检查：

1. 读取当前 diff，判断本次改动是否仅限于文档文件（`docs/`、`CLAUDE.md`、`README.md`、`.claude/`）。
   - 如果**仅改文档**：跳过 CHANGELOG 检查，直接 commit。
   - 如果**包含代码改动**：继续步骤 2。

2. 检查 `docs/CHANGELOG.md` 是否在本次 diff 中被修改：
   ```bash
   git diff HEAD --name-only | grep -q "docs/CHANGELOG.md"
   ```

3. 如果 `docs/CHANGELOG.md` **未被修改**：
   - **停止 commit 流程**
   - 向用户展示本次代码改动的摘要
   - 询问用户：「本次改动尚未记录到 docs/CHANGELOG.md，是否需要我帮你补充更新记录？」
   - 如果用户同意：按照以下格式追加记录到 `docs/CHANGELOG.md` 对应日期段下，然后将 CHANGELOG 一起 staged 后再 commit
   - 如果用户明确拒绝：正常 commit，不阻塞

4. CHANGELOG 记录格式：
   ```markdown
   ### <type>(<scope>): <简要描述>
   - **分支**: `<branch>`
   - **类型**: <feat|fix|refactor|docs|perf>
   - **涉及文件**: `file1`, `file2`
   - **说明**: <一两句话描述改了什么、为什么改>
   - **关联计划**: <计划文档路径，无则写"无">
   ```

## Your task

Based on the above changes, create a single git commit. Use Chinese language for the commit message.

注意执行顺序：
1. 先做 CHANGELOG 检查（上述流程）
2. 检查通过后，再分析 diff 内容、拟定 commit message
3. 最后执行 `git add` + `git commit`
