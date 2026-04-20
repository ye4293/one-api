# Channel Affinity UI 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在渠道管理页面新增"渠道亲和性"配置 UI，支持查看/编辑亲和规则、开关、TTL，以及清空 Redis 缓存、刷新缓存统计。

**Architecture:**
- 后端：亲和配置序列化为 JSON 存入现有 Options 表（key: `ChannelAffinityConfig`），新增 `/api/affinity/config`（GET/PUT）和 `/api/affinity/cache`（GET stats / DELETE clear）共 3 个端点，均需 AdminAuth。
- 前端：在渠道列表工具栏添加"亲和性配置"按钮，点击弹出 Dialog，内含开关/TTL/规则表格，支持 JSON 模式与可视化模式切换。
- 前端通过 Next.js API Route `/api/affinity/*` 代理后端请求。

**Tech Stack:** Go (Gin), React 18, Next.js 14 App Router, shadcn/ui, axios

---

## Task 1: 后端 — 扩展 ChannelAffinitySetting 结构体

**Files:**
- Modify: `common/channel_affinity_config.go`

**Step 1: 在 ChannelAffinitySetting 添加两个新字段**

```go
type ChannelAffinitySetting struct {
    Enabled                 bool
    MaxSize                 int  // 内存最大条目数，0 表示后端默认 100000
    DefaultTTLSeconds       int
    SwitchAffinityOnSuccess bool // 亲和渠道失败重试到其他渠道成功后，是否更新亲和
    Rules                   []ChannelAffinityRule
}
```

同时在 `ChannelAffinityConfig` 初始值中补充：
```go
var ChannelAffinityConfig = ChannelAffinitySetting{
    Enabled:                 false,
    MaxSize:                 100000,
    DefaultTTLSeconds:       3600,
    SwitchAffinityOnSuccess: false,
    Rules: []ChannelAffinityRule{ ... /* 保持不变 */ },
}
```

**Step 2: 在文件末尾添加 JSON 序列化辅助函数**

```go
import "encoding/json"

// AffinityConfigToJSON 序列化为 JSON 字符串，失败返回空串
func AffinityConfigToJSON(cfg ChannelAffinitySetting) string {
    b, err := json.Marshal(cfg)
    if err != nil {
        return ""
    }
    return string(b)
}

// AffinityConfigFromJSON 反序列化，失败返回默认值
func AffinityConfigFromJSON(s string) (ChannelAffinitySetting, error) {
    var cfg ChannelAffinitySetting
    if err := json.Unmarshal([]byte(s), &cfg); err != nil {
        return ChannelAffinityConfig, err
    }
    return cfg, nil
}
```

**Step 3: 编译检查**
```bash
cd /Users/yueqingli/code/one-api && go build ./...
```

**Step 4: Commit**
```bash
git add common/channel_affinity_config.go
git commit -m "feat(affinity): 扩展 ChannelAffinitySetting 添加 MaxSize/SwitchAffinityOnSuccess 字段及 JSON 辅助函数"
```

---

## Task 2: 后端 — Option 系统集成亲和配置持久化

**Files:**
- Modify: `model/option.go`

**Step 1: 在 `InitOptionMap` 中注册默认值**

在 `config.OptionMap["ModelMetricsEnabled"]` 那行之后添加：
```go
config.OptionMap["ChannelAffinityConfig"] = common.AffinityConfigToJSON(common.ChannelAffinityConfig)
```

**Step 2: 在 `updateOptionMap` 的 switch 中添加处理**

在最后的 `return err` 之前，在 `case "ClaudeRequestHeaders":` 之后添加：
```go
case "ChannelAffinityConfig":
    cfg, parseErr := common.AffinityConfigFromJSON(value)
    if parseErr != nil {
        err = parseErr
    } else {
        common.ChannelAffinityConfig = cfg
    }
```

**Step 3: 编译检查**
```bash
cd /Users/yueqingli/code/one-api && go build ./...
```

**Step 4: Commit**
```bash
git add model/option.go
git commit -m "feat(affinity): 将 ChannelAffinityConfig 集成到 Option 系统，支持数据库持久化"
```

---

## Task 3: 后端 — 新增 Affinity API Controller

**Files:**
- Create: `controller/affinity.go`

**Step 1: 创建文件**

```go
package controller

import (
    "context"
    "encoding/json"
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/songquanpeng/one-api/common"
    "github.com/songquanpeng/one-api/model"
)

// GetAffinityConfig GET /api/affinity/config
func GetAffinityConfig(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "message": "",
        "data":    common.ChannelAffinityConfig,
    })
}

// UpdateAffinityConfig PUT /api/affinity/config
func UpdateAffinityConfig(c *gin.Context) {
    var cfg common.ChannelAffinitySetting
    if err := json.NewDecoder(c.Request.Body).Decode(&cfg); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数解析失败: " + err.Error()})
        return
    }
    jsonStr := common.AffinityConfigToJSON(cfg)
    if err := model.UpdateOption("ChannelAffinityConfig", jsonStr); err != nil {
        c.JSON(http.StatusOK, gin.H{"success": false, "message": "保存失败: " + err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "message": "保存成功"})
}

// GetAffinityCacheStats GET /api/affinity/cache — 返回缓存条目数
func GetAffinityCacheStats(c *gin.Context) {
    if !common.RedisEnabled || common.RDB == nil {
        c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"count": 0, "redis_enabled": false}})
        return
    }
    var count int64
    var cursor uint64
    const prefix = "channel_affinity:v1:*"
    for {
        keys, nextCursor, err := common.RDB.Scan(context.Background(), cursor, prefix, 100).Result()
        if err != nil {
            break
        }
        count += int64(len(keys))
        cursor = nextCursor
        if cursor == 0 {
            break
        }
    }
    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "data":    gin.H{"count": count, "redis_enabled": true},
    })
}

// ClearAffinityCache DELETE /api/affinity/cache — 清空所有亲和缓存
func ClearAffinityCache(c *gin.Context) {
    if !common.RedisEnabled || common.RDB == nil {
        c.JSON(http.StatusOK, gin.H{"success": true, "message": "Redis 未启用，无需清理"})
        return
    }
    var cursor uint64
    const prefix = "channel_affinity:v1:*"
    var deleted int64
    for {
        keys, nextCursor, err := common.RDB.Scan(context.Background(), cursor, prefix, 100).Result()
        if err != nil {
            c.JSON(http.StatusOK, gin.H{"success": false, "message": "扫描失败: " + err.Error()})
            return
        }
        if len(keys) > 0 {
            if err := common.RDB.Del(context.Background(), keys...).Err(); err != nil {
                c.JSON(http.StatusOK, gin.H{"success": false, "message": "删除失败: " + err.Error()})
                return
            }
            deleted += int64(len(keys))
        }
        cursor = nextCursor
        if cursor == 0 {
            break
        }
    }
    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "message": "已清空全部亲和缓存",
        "data":    gin.H{"deleted": deleted},
    })
}
```

**Step 2: 编译检查**
```bash
cd /Users/yueqingli/code/one-api && go build ./...
```

**Step 3: Commit**
```bash
git add controller/affinity.go
git commit -m "feat(affinity): 新增 GetAffinityConfig/UpdateAffinityConfig/GetAffinityCacheStats/ClearAffinityCache 接口"
```

---

## Task 4: 后端 — 注册路由

**Files:**
- Modify: `router/api-router.go`

**Step 1: 在 channelRoute 组之后添加 affinityRoute**

找到 `channelRoute.PUT("/multi-key/settings"...` 那行之后，在 `tokenRoute` 之前插入：

```go
affinityRoute := apiRouter.Group("/affinity")
affinityRoute.Use(middleware.AdminAuth())
{
    affinityRoute.GET("/config", controller.GetAffinityConfig)
    affinityRoute.PUT("/config", controller.UpdateAffinityConfig)
    affinityRoute.GET("/cache", controller.GetAffinityCacheStats)
    affinityRoute.DELETE("/cache", controller.ClearAffinityCache)
}
```

**Step 2: 编译检查**
```bash
cd /Users/yueqingli/code/one-api && go build ./... && go vet ./...
```

**Step 3: Commit**
```bash
git add router/api-router.go
git commit -m "feat(affinity): 注册 /api/affinity 路由组"
```

---

## Task 5: 前端 — 新增 Next.js API 代理路由

**Files:**
- Create: `app/api/affinity/config/route.ts`
- Create: `app/api/affinity/cache/route.ts`

**Step 1: 创建 config 代理**

`app/api/affinity/config/route.ts`:
```typescript
import { ApiHandler } from '@/app/lib/api-handler';

const handler = new ApiHandler({
  endpoint: '/api/affinity/config',
  requireAuth: true
});

export const GET = handler.get;
export const PUT = handler.put;
```

**Step 2: 创建 cache 代理**

`app/api/affinity/cache/route.ts`:
```typescript
import { ApiHandler } from '@/app/lib/api-handler';

const handler = new ApiHandler({
  endpoint: '/api/affinity/cache',
  requireAuth: true
});

export const GET = handler.get;
export const DELETE = handler.delete;
```

**Step 3: Commit**
```bash
cd /Users/yueqingli/code/ezlinkai-web
git add app/api/affinity/
git commit -m "feat(affinity): 新增 Next.js API 代理路由 /api/affinity/config 和 /api/affinity/cache"
```

---

## Task 6: 前端 — 创建 AffinityModal 组件

**Files:**
- Create: `sections/channel/affinity-modal.tsx`

这是核心 UI 组件，参照截图实现。完整代码：

```typescript
'use client';
import React, { useState, useEffect, useCallback } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from '@/components/ui/table';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Info, Plus, RefreshCw, Trash2, Pencil, X } from 'lucide-react';
import { toast } from 'sonner';
import request from '@/app/lib/clientFetch';

// ─── 类型定义 ─────────────────────────────────────────────────────────────────

interface KeySource {
  Type: string;   // "context_int" | "context_string" | "gjson"
  Key: string;
  Path: string;
}

interface AffinityRule {
  Name: string;
  ModelRegex: string[];
  PathRegex: string[];
  UserAgentInclude: string[];
  KeySources: KeySource[];
  ValueRegex: string;
  TTLSeconds: number;
  SkipRetryOnFailure: boolean;
  IncludeRuleName: boolean;
  IncludeModelName: boolean;
  IncludeUsingGroup: boolean;
}

interface AffinityConfig {
  Enabled: boolean;
  MaxSize: number;
  DefaultTTLSeconds: number;
  SwitchAffinityOnSuccess: boolean;
  Rules: AffinityRule[];
}

const DEFAULT_CONFIG: AffinityConfig = {
  Enabled: false,
  MaxSize: 100000,
  DefaultTTLSeconds: 3600,
  SwitchAffinityOnSuccess: false,
  Rules: []
};

// ─── 规则编辑弹窗 ─────────────────────────────────────────────────────────────

function RuleEditDialog({
  open,
  onOpenChange,
  rule,
  onSave
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  rule: AffinityRule | null;
  onSave: (r: AffinityRule) => void;
}) {
  const [form, setForm] = useState<AffinityRule>(
    rule ?? {
      Name: '',
      ModelRegex: [],
      PathRegex: [],
      UserAgentInclude: [],
      KeySources: [{ Type: 'gjson', Key: '', Path: '' }],
      ValueRegex: '',
      TTLSeconds: 0,
      SkipRetryOnFailure: true,
      IncludeRuleName: true,
      IncludeModelName: false,
      IncludeUsingGroup: true
    }
  );

  useEffect(() => {
    if (rule) setForm(rule);
  }, [rule]);

  const handle = (field: keyof AffinityRule, value: any) =>
    setForm((prev) => ({ ...prev, [field]: value }));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{rule ? '编辑规则' : '新增规则'}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1">
              <Label>规则名称</Label>
              <Input value={form.Name} onChange={(e) => handle('Name', e.target.value)} placeholder="如 claude-cli" />
            </div>
            <div className="space-y-1">
              <Label>TTL（秒，0 使用全局默认）</Label>
              <Input type="number" value={form.TTLSeconds} onChange={(e) => handle('TTLSeconds', Number(e.target.value))} />
            </div>
          </div>
          <div className="space-y-1">
            <Label>模型正则（每行一条）</Label>
            <Textarea
              rows={2}
              value={form.ModelRegex.join('\n')}
              onChange={(e) => handle('ModelRegex', e.target.value.split('\n').filter(Boolean))}
              placeholder="^claude-"
            />
          </div>
          <div className="space-y-1">
            <Label>路径正则（每行一条，为空不限制）</Label>
            <Textarea
              rows={2}
              value={form.PathRegex.join('\n')}
              onChange={(e) => handle('PathRegex', e.target.value.split('\n').filter(Boolean))}
              placeholder="/v1/messages"
            />
          </div>
          <div className="space-y-1">
            <Label>Key 来源（仅支持一条 gjson，Path 如 metadata.user_id）</Label>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <Label className="text-xs text-muted-foreground">类型</Label>
                <Input
                  value={form.KeySources[0]?.Type ?? 'gjson'}
                  onChange={(e) => {
                    const sources = [...form.KeySources];
                    sources[0] = { ...sources[0], Type: e.target.value };
                    handle('KeySources', sources);
                  }}
                  placeholder="gjson"
                />
              </div>
              <div className="space-y-1">
                <Label className="text-xs text-muted-foreground">路径 / Key</Label>
                <Input
                  value={form.KeySources[0]?.Path || form.KeySources[0]?.Key || ''}
                  onChange={(e) => {
                    const sources = [...form.KeySources];
                    const t = sources[0]?.Type ?? 'gjson';
                    sources[0] = {
                      ...sources[0],
                      Path: t === 'gjson' ? e.target.value : '',
                      Key: t !== 'gjson' ? e.target.value : ''
                    };
                    handle('KeySources', sources);
                  }}
                  placeholder="metadata.user_id"
                />
              </div>
            </div>
          </div>
          <div className="flex items-center gap-6">
            <div className="flex items-center gap-2">
              <Switch
                checked={form.SkipRetryOnFailure}
                onCheckedChange={(v) => handle('SkipRetryOnFailure', v)}
              />
              <Label>失败后不重试</Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch checked={form.IncludeRuleName} onCheckedChange={(v) => handle('IncludeRuleName', v)} />
              <Label>Key 含规则名</Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch checked={form.IncludeModelName} onCheckedChange={(v) => handle('IncludeModelName', v)} />
              <Label>Key 含模型名</Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch checked={form.IncludeUsingGroup} onCheckedChange={(v) => handle('IncludeUsingGroup', v)} />
              <Label>Key 含分组</Label>
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button onClick={() => { onSave(form); onOpenChange(false); }}>保存规则</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ─── 主组件 ───────────────────────────────────────────────────────────────────

export default function AffinityModal({
  open,
  onOpenChange
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const [config, setConfig] = useState<AffinityConfig>(DEFAULT_CONFIG);
  const [jsonMode, setJsonMode] = useState(false);
  const [jsonText, setJsonText] = useState('');
  const [cacheCount, setCacheCount] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [ruleDialogOpen, setRuleDialogOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<AffinityRule | null>(null);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);

  const fetchConfig = useCallback(async () => {
    try {
      const res = await request.get('/api/affinity/config');
      if (res.data?.success) {
        const cfg = res.data.data as AffinityConfig;
        setConfig(cfg);
        setJsonText(JSON.stringify(cfg, null, 2));
      }
    } catch (e) {
      toast.error('加载亲和配置失败');
    }
  }, []);

  const fetchCacheStats = useCallback(async () => {
    try {
      const res = await request.get('/api/affinity/cache');
      if (res.data?.success) {
        setCacheCount(res.data.data?.count ?? 0);
      }
    } catch {}
  }, []);

  useEffect(() => {
    if (open) {
      fetchConfig();
      fetchCacheStats();
    }
  }, [open, fetchConfig, fetchCacheStats]);

  const handleSave = async () => {
    setSaving(true);
    try {
      let cfg = config;
      if (jsonMode) {
        cfg = JSON.parse(jsonText);
      }
      const res = await request.put('/api/affinity/config', cfg);
      if (res.data?.success) {
        toast.success('保存成功');
        setConfig(cfg);
      } else {
        toast.error(res.data?.message ?? '保存失败');
      }
    } catch (e: any) {
      toast.error('保存失败: ' + (e.message ?? ''));
    } finally {
      setSaving(false);
    }
  };

  const handleClearCache = async () => {
    setClearing(true);
    try {
      const res = await request.delete('/api/affinity/cache');
      if (res.data?.success) {
        toast.success(res.data.message ?? '缓存已清空');
        setCacheCount(0);
      } else {
        toast.error(res.data?.message ?? '清空失败');
      }
    } catch {
      toast.error('清空缓存失败');
    } finally {
      setClearing(false);
    }
  };

  const handleSwitchToJson = () => {
    setJsonText(JSON.stringify(config, null, 2));
    setJsonMode(true);
  };

  const handleSwitchToVisual = () => {
    try {
      const cfg = JSON.parse(jsonText);
      setConfig(cfg);
      setJsonMode(false);
    } catch {
      toast.error('JSON 格式错误，无法切换');
    }
  };

  const handleAddRule = () => {
    setEditingRule(null);
    setEditingIndex(null);
    setRuleDialogOpen(true);
  };

  const handleEditRule = (rule: AffinityRule, index: number) => {
    setEditingRule(rule);
    setEditingIndex(index);
    setRuleDialogOpen(true);
  };

  const handleDeleteRule = (index: number) => {
    setConfig((prev) => ({
      ...prev,
      Rules: prev.Rules.filter((_, i) => i !== index)
    }));
  };

  const handleSaveRule = (rule: AffinityRule) => {
    setConfig((prev) => {
      const rules = [...(prev.Rules ?? [])];
      if (editingIndex !== null) {
        rules[editingIndex] = rule;
      } else {
        rules.push(rule);
      }
      return { ...prev, Rules: rules };
    });
  };

  const formatKeySource = (sources: KeySource[]) => {
    if (!sources?.length) return '-';
    const s = sources[0];
    if (s.Type === 'gjson') return `gjson:${s.Path}`;
    return `${s.Type}:${s.Key}`;
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[90vh] max-w-5xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>渠道亲和性</DialogTitle>
          </DialogHeader>

          <Alert className="border-blue-200 bg-blue-50">
            <Info className="h-4 w-4 text-blue-600" />
            <AlertDescription className="text-blue-700">
              渠道亲和性会基于从请求上下文或 JSON Body 提取的 Key，优先复用上一次成功的渠道。
            </AlertDescription>
          </Alert>

          {!jsonMode ? (
            <div className="space-y-6">
              {/* 全局开关 + 参数 */}
              <div className="grid grid-cols-3 gap-6">
                <div className="space-y-2">
                  <Label className="text-base font-semibold">启用</Label>
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={config.Enabled}
                      onCheckedChange={(v) => setConfig((prev) => ({ ...prev, Enabled: v }))}
                    />
                  </div>
                  <p className="text-xs text-muted-foreground">启用后将优先复用上一次成功的渠道（粘滞选路）。</p>
                </div>
                <div className="space-y-2">
                  <Label className="text-base font-semibold">最大条目数</Label>
                  <Input
                    type="number"
                    value={config.MaxSize}
                    onChange={(e) => setConfig((prev) => ({ ...prev, MaxSize: Number(e.target.value) }))}
                    className="w-36"
                  />
                  <p className="text-xs text-muted-foreground">内存存储最大条目数。0 表示使用后端默认容量：100000。</p>
                </div>
                <div className="space-y-2">
                  <Label className="text-base font-semibold">默认 TTL（秒）</Label>
                  <Input
                    type="number"
                    value={config.DefaultTTLSeconds}
                    onChange={(e) => setConfig((prev) => ({ ...prev, DefaultTTLSeconds: Number(e.target.value) }))}
                    className="w-36"
                  />
                  <p className="text-xs text-muted-foreground">规则 ttl_seconds 为 0 时使用。0 表示使用后端默认 TTL：3600 秒。</p>
                </div>
              </div>

              {/* 成功后切换亲和 */}
              <div className="space-y-2">
                <Label className="text-base font-semibold">成功后切换亲和</Label>
                <div className="flex items-center gap-2">
                  <Switch
                    checked={config.SwitchAffinityOnSuccess}
                    onCheckedChange={(v) => setConfig((prev) => ({ ...prev, SwitchAffinityOnSuccess: v }))}
                  />
                </div>
                <p className="text-xs text-muted-foreground">
                  如果亲和到的渠道失败，重试到其他渠道成功后，将亲和更新到成功的渠道。
                </p>
              </div>

              {/* 工具栏 */}
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" onClick={handleSwitchToJson}>JSON 模式</Button>
                <Button variant="outline" size="sm" onClick={handleAddRule}>
                  <Plus className="mr-1 h-3 w-3" /> 新增规则
                </Button>
                <Button size="sm" onClick={handleSave} disabled={saving}>
                  {saving ? '保存中...' : '保存'}
                </Button>
                <Button variant="outline" size="sm" onClick={fetchCacheStats}>
                  <RefreshCw className="mr-1 h-3 w-3" />
                  刷新缓存统计
                  {cacheCount !== null && <span className="ml-1 text-muted-foreground">({cacheCount})</span>}
                </Button>
                <Button variant="destructive" size="sm" onClick={handleClearCache} disabled={clearing}>
                  {clearing ? '清空中...' : '清空全部缓存'}
                </Button>
              </div>

              {/* 规则表格 */}
              <div className="rounded-md border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>名称</TableHead>
                      <TableHead>模型正则</TableHead>
                      <TableHead>路径正则</TableHead>
                      <TableHead>Key 来源</TableHead>
                      <TableHead>TTL（秒）</TableHead>
                      <TableHead>失败后是否重试</TableHead>
                      <TableHead>操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(config.Rules ?? []).length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                          暂无规则，点击"新增规则"添加
                        </TableCell>
                      </TableRow>
                    ) : (
                      (config.Rules ?? []).map((rule, idx) => (
                        <TableRow key={idx}>
                          <TableCell className="font-medium">{rule.Name}</TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-1">
                              {rule.ModelRegex?.map((r, i) => (
                                <Badge key={i} variant="secondary" className="font-mono text-xs">{r}</Badge>
                              ))}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-1">
                              {rule.PathRegex?.length ? rule.PathRegex.map((r, i) => (
                                <Badge key={i} variant="outline" className="font-mono text-xs">{r}</Badge>
                              )) : <span className="text-muted-foreground">-</span>}
                            </div>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{formatKeySource(rule.KeySources)}</TableCell>
                          <TableCell>{rule.TTLSeconds > 0 ? rule.TTLSeconds : '-'}</TableCell>
                          <TableCell>
                            {rule.SkipRetryOnFailure ? (
                              <Badge variant="destructive" className="text-xs">不重试</Badge>
                            ) : (
                              <Badge variant="secondary" className="text-xs">重试</Badge>
                            )}
                          </TableCell>
                          <TableCell>
                            <div className="flex items-center gap-1">
                              <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => handleEditRule(rule, idx)}>
                                <Pencil className="h-3.5 w-3.5" />
                              </Button>
                              <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={() => handleDeleteRule(idx)}>
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </div>
            </div>
          ) : (
            /* JSON 模式 */
            <div className="space-y-4">
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={handleSwitchToVisual}>可视化模式</Button>
                <Button size="sm" onClick={handleSave} disabled={saving}>{saving ? '保存中...' : '保存'}</Button>
              </div>
              <Textarea
                className="min-h-[400px] font-mono text-sm"
                value={jsonText}
                onChange={(e) => setJsonText(e.target.value)}
              />
            </div>
          )}
        </DialogContent>
      </Dialog>

      <RuleEditDialog
        open={ruleDialogOpen}
        onOpenChange={setRuleDialogOpen}
        rule={editingRule}
        onSave={handleSaveRule}
      />
    </>
  );
}
```

**Step 2: Commit**
```bash
cd /Users/yueqingli/code/ezlinkai-web
git add sections/channel/affinity-modal.tsx
git commit -m "feat(affinity): 新增 AffinityModal 组件，支持可视化/JSON 双模式编辑亲和规则"
```

---

## Task 7: 前端 — 在渠道列表工具栏添加亲和性按钮

**Files:**
- Modify: `sections/channel/tables/index.tsx`

**Step 1: 添加 import**

在文件顶部 import 区域添加：
```typescript
import AffinityModal from '../affinity-modal';
import { Settings2 } from 'lucide-react';
```

**Step 2: 添加 state**

在 `const [isMultiKeyModalOpen, ...]` 那行附近添加：
```typescript
const [isAffinityModalOpen, setIsAffinityModalOpen] = useState(false);
```

**Step 3: 在 JSX 中渲染 Modal**

在 `{isMultiKeyModalOpen && (` 的那个 `<MultiKeyManagementModal .../>` 块下方添加：
```tsx
<AffinityModal
  open={isAffinityModalOpen}
  onOpenChange={setIsAffinityModalOpen}
/>
```

**Step 4: 在搜索栏右侧添加按钮**

找到 `<DataTableSearch` 所在的 `<div>` 块，在 `<DataTableResetFilter` 之前插入：
```tsx
<Button
  variant="outline"
  size="sm"
  onClick={() => setIsAffinityModalOpen(true)}
>
  <Settings2 className="mr-2 h-4 w-4" />
  亲和性配置
</Button>
```

**Step 5: Commit**
```bash
cd /Users/yueqingli/code/ezlinkai-web
git add sections/channel/tables/index.tsx
git commit -m "feat(affinity): 在渠道列表工具栏添加亲和性配置入口按钮"
```

---

## Task 8: 验证

**Step 1: 后端启动验证**
```bash
cd /Users/yueqingli/code/one-api
go build ./... && go vet ./...
```

**Step 2: 前端类型检查**
```bash
cd /Users/yueqingli/code/ezlinkai-web
npx tsc --noEmit 2>&1 | head -50
```

**Step 3: 手动测试流程**
1. 启动后端 `go run main.go`
2. 启动前端 `pnpm dev`
3. 进入渠道管理页，点击"亲和性配置"按钮
4. 验证：加载现有配置 ✓、修改启用开关保存 ✓、新增/编辑/删除规则 ✓、JSON 模式切换 ✓、刷新缓存统计 ✓、清空缓存 ✓
