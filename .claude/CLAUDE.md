# one-api 项目开发规范

## 项目结构
- `controller/` - HTTP 处理层
- `model/` - 数据库模型
- `relay/` - 各渠道适配器（含 VideoAdaptor）
- `common/` - 公共工具（config、logger、helper）
- `web/default/` - React 前端
- `.github/workflows/` - CI/CD（docker-dev.yml 触发分支：`dev`）

## 常用命令
```bash
go build ./...        # 编译检查（提交前必跑）
go vet ./...          # 静态分析
go test ./...         # 运行测试
```

## 提交前必须执行
每次修改 Go 代码后，提交前**必须**执行，不能跳过：
```bash
go build ./... && go vet ./...
```

## 工作流技巧

**多分支并行开发用 Git Worktree**（不要切换分支）：
```bash
git worktree add /tmp/dev-test dev   # 检出 dev 分支到临时目录
cd /tmp/dev-test && go build ./...   # 独立环境验证
git worktree remove /tmp/dev-test    # 用完清理
```

**复杂任务先进 Plan Mode**，列出所有步骤确认后再执行。

**上下文优于指令**：提问时说明当前分支、涉及模块、参考已有实现，效果远好于只说"加个功能"。

**定期 `/compact`** 压缩对话历史释放 token。

**每完成一个独立功能就 commit**，粒度小、回滚容易。

## 错误处理模式
遇到 CI / 编译 / 运行时报错：
1. 读取完整错误信息（不要跳过 stack trace）
2. 定位根本原因（区分"哪个分支""哪个文件"）
3. 最小化修复并本地验证
4. 更新此文件记录教训
