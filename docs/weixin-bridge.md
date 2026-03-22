# 微信接入指南

## 概述

scAgent 内置微信桥接功能，基于腾讯 iLink Bot 协议（纯 Go 实现，无 Node.js 依赖），用户在微信中发送分析请求，系统返回文字结果。

iLink 是腾讯通过 [OpenClaw](https://docs.openclaw.ai) 框架正式开放的微信个人 Bot API，接入域名 `ilinkai.weixin.qq.com`，有《微信ClawBot功能使用条款》法律文件背书，非灰色协议。

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

Go 版本直接嵌入控制平面进程：
- 无 HTTP 中转，直接调用 `service.SubmitMessage()` 和 `service.Subscribe()`
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

---

## iLink Bot 协议参考

以下是 scAgent 微信桥接所依赖的 iLink 协议核心技术细节。

### API 列表

基础地址：`https://ilinkai.weixin.qq.com`

| Endpoint | Method | 功能 |
|---|---|---|
| `/ilink/bot/get_bot_qrcode` | GET | 获取登录二维码（`?bot_type=3`） |
| `/ilink/bot/get_qrcode_status` | GET | 轮询扫码状态（`?qrcode=xxx`） |
| `/ilink/bot/getupdates` | POST | 长轮询收消息（hold 最多 35 秒） |
| `/ilink/bot/sendmessage` | POST | 发送消息 |
| `/ilink/bot/getuploadurl` | POST | 获取 CDN 预签名上传地址 |
| `/ilink/bot/getconfig` | POST | 获取 typing_ticket |
| `/ilink/bot/sendtyping` | POST | 发送「正在输入」状态 |

### 请求头

```
Content-Type: application/json
AuthorizationType: ilink_bot_token
X-WECHAT-UIN: base64(String(randomUint32()))   // 每次随机，防重放
Authorization: Bearer <bot_token>               // 登录后获得
```

### 鉴权流程

```
开发者                    iLink 服务器              微信用户
  │── GET get_bot_qrcode ──▶│                        │
  │◀── { qrcode, url } ─────│                        │
  │                          │◀─── 用户扫码 ──────────│
  │── GET get_qrcode_status ─▶│（轮询）                │
  │◀── { status: "confirmed", │                        │
  │      bot_token, baseurl } │                        │
  │                          │                        │
  │  持久化 bot_token，后续 Bearer 鉴权               │
```

### 消息结构

```json
{
  "from_user_id": "xxx@im.wechat",
  "to_user_id": "xxx@im.bot",
  "message_type": 1,
  "message_state": 2,
  "context_token": "AARzJWAF...",
  "item_list": [
    { "type": 1, "text_item": { "text": "你好" } }
  ]
}
```

**消息类型（`item_list[].type`）：** 1=文本、2=图片、3=语音、4=文件、5=视频

**`context_token`** 是协议中最关键的字段——回复时必须原样携带，否则消息不会关联到正确的对话窗口。

### 长轮询

```json
POST /ilink/bot/getupdates
{
  "get_updates_buf": "<上次返回的游标>",
  "base_info": { "channel_version": "1.0.2" }
}
```

服务器 hold 连接最多 35 秒。`get_updates_buf` 类似数据库 cursor，必须每次更新，否则重复收消息。

### 媒体文件

CDN 域名 `novac2c.cdn.weixin.qq.com/c2c`，所有媒体经 AES-128-ECB 加密。发送图片流程：生成随机 AES key → 加密文件 → `getuploadurl` 获取预签名 URL → PUT 到 CDN → `sendmessage` 带上 `aes_key`。

### 参考实现

scAgent 的 Go 实现参考了 [weixin-agent-sdk](https://github.com/wong2/weixin-agent-sdk) 的 TypeScript 源码，关键文件：

| 功能 | 参考文件 |
|------|----------|
| QR 登录流程 | `packages/sdk/src/auth/login-qr.ts` — `qrcode_img_content` 字段即为需要渲染成 QR 码的 URL |
| 账号持久化 | `packages/sdk/src/auth/accounts.ts` — token/baseUrl 存储 |
| 长轮询主循环 | `packages/sdk/src/monitor/monitor.ts` — getUpdates + 消息分发 |
| 消息处理 | `packages/sdk/src/messaging/process-message.ts` — inbound 消息解析 |
| 消息发送 | `packages/sdk/src/messaging/send.ts` — sendmessage + context_token |
| CDN 媒体加解密 | `packages/sdk/src/cdn/aes-ecb.ts` — AES-128-ECB |
| API 封装 | `packages/sdk/src/api/api.ts` — 请求头构造、X-WECHAT-UIN 生成 |

遇到协议问题时，优先查阅上述文件获取最新实现细节。

### 合规要点

- 腾讯定位为「管道」，不存储消息内容，不提供 AI 服务
- 腾讯保留限速、内容过滤、终止连接的权利
- IP/设备信息会被收集用于安全审计
- 禁止绕过微信技术保护措施
- 腾讯可随时变更或终止服务，**不应将核心业务完全依赖此 API**

---

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
