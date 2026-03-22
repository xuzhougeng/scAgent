# 微信接入指南

## 概述

scAgent 内置微信桥接功能，基于腾讯 iLink Bot 协议（纯 Go 实现，无 Node.js 依赖），用户在微信中发送分析请求，系统返回文字结果。

## 快速开始

### 1. 微信扫码登录

```bash
make weixin-login
```

终端会打印二维码链接，用微信扫码完成登录。登录凭证保存在 `data/weixin-bridge/account.json`，后续启动无需重复扫码。

### 2. 启动

**方式 A：随 make dev 一起启动**

在 `.env` 中设置：

```
WEIXIN_BRIDGE_ENABLED=1
```

然后正常启动：

```bash
make dev
```

**方式 B：专用 make 命令**

```bash
make weixin
```

等同于 `WEIXIN_BRIDGE_ENABLED=1 ./start.sh`。

## 配置项

所有配置通过环境变量或 `.env` 文件设置：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WEIXIN_BRIDGE_ENABLED` | `0` | 是否启用微信桥接 |
| `WEIXIN_BRIDGE_TIMEOUT_MS` | `300000` | 单次分析任务超时（毫秒），默认 5 分钟 |
| `WEIXIN_BRIDGE_SESSION_LABEL` | `WeChat` | 新建 session 的标签前缀 |

## 微信斜杠命令

在微信中发送以下命令管理会话：

| 命令 | 说明 |
|------|------|
| `/help` | 显示可用命令 |
| `/status` | 查看当前会话状态（会话ID、工作区、对象数等） |
| `/workspaces` | 列出所有工作区，当前工作区用 `→` 标记 |
| `/switch <workspace_id>` | 切换到指定工作区（在该工作区新建对话） |
| `/new` | 创建新工作区+会话 |
| `/reset` | 重置会话映射，下次消息自动加入最近的工作区 |

## 工作原理

```
微信用户 ←→ iLink Bot API（长轮询）
                  ↓
            Go Bridge（同进程）
                  ↓
          orchestrator.Service（直接调用）
                  ↓
            scAgent 控制平面
```

与 TypeScript SDK 方案不同，Go 版本直接嵌入控制平面进程：
- 无 HTTP 中转，直接调用 `service.SubmitMessage()` 和 `service.Subscribe()`
- 无需额外的 Node.js 运行时
- 微信桥接作为 goroutine 运行，随主进程启停

### 消息处理流程

1. 每个微信用户（`from_user_id`）映射到一个 scAgent session
2. 新用户自动加入最近访问的 workspace（复用已有数据）
3. 用户发送文字 → 桥接调用 `SubmitMessage`
4. 桥接通过 `Subscribe` 监听 job 事件
5. job 完成后，提取 assistant 回复发回微信
6. 回复时携带 `context_token`，确保消息关联到正确的对话窗口

## 会话与数据持久化

| 文件 | 说明 |
|------|------|
| `data/weixin-bridge/account.json` | 微信登录凭证（bot_token） |
| `data/weixin-bridge/sessions.json` | 微信用户 → scAgent session 映射 |
| `data/weixin-bridge/sync_buf` | 长轮询游标，重启后断点续传 |

## 常见问题

**Q: 扫码后提示登录失败？**
A: 重新运行 `make weixin-login`。

**Q: 发消息后长时间无回复？**
A: 部分分析任务可能耗时较长，默认超时 5 分钟。用 `/status` 检查会话状态。

**Q: 如何切换到另一个已有数据的工作区？**
A: 发送 `/workspaces` 查看列表，然后 `/switch ws_000001` 切换。

**Q: 会话过期怎么办？**
A: 桥接会检测 errcode -14 并提示需要重新登录。重新运行 `make weixin-login`。

**Q: 如何同时使用 Web 界面和微信？**
A: 两者完全独立。微信用户会创建独立的 session。通过 `/switch` 命令可以切换到 Web 界面正在使用的 workspace。
