# 微信接入指南

## 概述

scAgent 内置微信桥接功能，基于腾讯 iLink Bot 协议（纯 Go 实现，无 Node.js 依赖），用户在微信中发送分析请求，系统返回文字结果；若分析生成了 plot artifact，还会直接回传图片。

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

| 命令 | 简写 | 说明 |
|------|------|------|
| `/help` | `/h` | 显示可用命令 |
| `/status` | | 查看当前会话状态（会话ID、工作区、对象数等） |
| `/list` | `/l` | 列出所有工作区，当前工作区用 `→` 标记 |
| `/switch <序号>` | `/s <序号>` | 按序号切换工作区（如 `/s 1`），也支持 workspace_id |
| `/list-chat` | `/lc` | 列出当前工作区的所有对话 |
| `/switch-chat <序号>` | `/sc <序号>` | 按序号切换对话（如 `/sc 1`） |
| `/new-chat` | `/nc` | 在当前工作区新建对话 |
| `/new` | | 创建新工作区+会话 |
| `/reset` | | 重置会话映射，下次消息自动加入最近的工作区 |

## 语音消息支持

微信语音消息开箱即用，无需额外配置。处理流程：

1. **微信服务端自动转写**：用户发送语音后，微信服务器完成语音识别，转写结果存放在 `voice_item.text` 字段中，随 `getUpdates` 一起返回
2. **桥接提取文字**：`extractText()` 检测到 `item_list[].type == 3`（语音）时，直接取 `voice_item.text` 作为消息正文，后续流程与普通文字消息完全一致
3. **无转写文本的语音**：如果微信识别失败（`voice_item.text` 为空），该消息会被跳过

> 语音转写由微信完成，scAgent 不做任何语音识别处理。

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
5. 若消息里带文件，桥接会先从 CDN 下载并解密，再按文件类型分类处理
6. `h5ad` 文件会保存为会话 artifact，作为后续主流程里“转换 / 导入”任务的锚点；桥接层本身不会直接载入运行时
7. `csv/tsv` 文件会保存为会话 artifact，并生成轻量表格摘要（行数、列数、前 10 行、前 5 列、列名识别）提供给模型
8. job 完成后，提取 assistant 回复；若有 plot artifact，则继续上传 CDN 并回传图片
9. 回复时携带 `context_token`，确保消息关联到正确的对话窗口

当前实现中：
- 收到 `h5ad` 文件时，会将文件保存到当前 workspace 的 artifact 目录，并把它作为后续数据转换 / 导入步骤的锚点提供给模型，不会在桥接层直接改写当前会话状态。
- 收到 `csv/tsv` 文件时，会将文件保存到当前 workspace 的 artifact 目录，并抽取轻量摘要给模型，默认只看前 10 行、前 5 列，并尽量识别首行列名，适合 marker 基因列表、表格筛选等交互。
- 回复时会先发送文字摘要，再按生成顺序发送该 job 的 plot PNG 图片。

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

### 发送消息必填字段

```json
POST /ilink/bot/sendmessage
{
  "msg": {
    "to_user_id": "xxx@im.wechat",
    "client_id": "openclaw-weixin-1742000000000-12345",
    "message_type": 2,
    "message_state": 2,
    "context_token": "AARzJWAF...",
    "item_list": [{ "type": 1, "text_item": { "text": "回复内容" } }]
  },
  "base_info": { "channel_version": "1.0.2" }
}
```

**关键踩坑点：**

- **`client_id`（必须）**：每条消息必须携带唯一 ID（格式 `openclaw-weixin-<timestamp>-<random>`）。缺失时服务端会将后续消息视为重复并静默丢弃，导致"第一条消息能回复，后续全部无响应"。参见 `send.ts` 中的 `generateId()`。
- **`base_info`（必须）**：所有 POST 请求（`sendmessage`、`sendtyping`、`getconfig`、`getupdates`）都必须在请求体顶层携带 `base_info: { channel_version: "1.0.2" }`，不仅是 `getupdates`。参见 `api.ts` 中所有请求均追加 `base_info`。
- **`sendtyping` 字段差异**：收件人字段为 `ilink_user_id`（非 `to_user_id`），且必须包含 `status: 1`（typing）或 `status: 2`（cancel）。
- **`getconfig` 需要用户信息**：必须传 `ilink_user_id` 和 `context_token`，空 body 可能返回无效 ticket。

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

CDN 域名 `novac2c.cdn.weixin.qq.com/c2c`，所有媒体经 AES-128-ECB 加密。发送图片流程：生成随机 AES key → 加密文件 → `getuploadurl` 获取预签名 URL → `POST` 到 CDN → `sendmessage` 带上 `aes_key`。

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

## 主动消息推送

微信桥接支持在特定时机主动向用户推送消息，无需用户先发消息触发。

### 首次会话欢迎消息

当微信用户首次与 Bot 交互时，在处理用户消息之前自动推送欢迎/帮助文档，让用户立刻了解 Bot 的能力和使用方式。

**触发条件**：`resolveSession` 检测到该用户没有已缓存的 session，需要新建。

**实现要点**：

1. `resolveSession` 返回 `isNew` 标记，区分首次用户 vs 回归用户
2. `handleMessage` 检测到 `isNew=true` 时，用 goroutine 先推送欢迎消息
3. 短暂延迟（200ms）确保欢迎消息在任务结果之前到达

**消息时序**：

```
用户第一条消息 "分析我的数据"
  │
  ├─① resolveSession → 新建 session, isNew=true
  ├─② goroutine: SendTextMessage(welcomeMessage)   ← 欢迎消息先发
  ├─③ sleep 200ms（确保顺序）
  ├─④ 正常处理用户消息 → 提交任务 → 返回结果
  │
  用户收到:
    [欢迎消息]          ← 先到
    [任务结果/等待提示]  ← 后到
```

**改动范围**：全部在 `internal/weixin/bridge.go`，约 35 行：

| 改动 | 说明 |
|------|------|
| `resolveSession` 签名 | 返回值加 `isNew bool` |
| `welcomeMessage` 常量 | 定义欢迎文本（命令列表 + 首次使用提示） |
| `handleMessage` 推送逻辑 | 检测 `isNew`，goroutine 发送，200ms 延迟 |
| 重试路径适配 | 第二处 `resolveSession` 调用忽略 `isNew`（`_, _,` 丢弃） |

### 扩展方向

- **可配置消息**：将欢迎文本改为从 `BridgeConfig.WelcomeMessage` 读取，为空则不发送
- **定向推送 API**：新增 `POST /api/push` 端点，支持向指定微信用户推送通知（需额外维护 `weixinUserID → contextToken` 映射）
- **广播推送**：遍历 `sessions.json` 中所有已知用户批量发送公告

---

## TODO

- [x] **图片发送**：分析生成的 plot 图片会发回微信（AES-128-ECB 加密 → `getuploadurl` → `POST` CDN → `sendmessage`），实现参考 `cdn/aes-ecb.ts`
- [x] **h5ad 文件接收**：解析用户发来的 `.h5ad` 文件（CDN 下载 + AES 解密），并导入当前会话作为输入数据
- [x] **CSV/TSV 文件接收**：解析用户发来的 `.csv` / `.tsv` 文件（CDN 下载 + AES 解密），保存为会话 artifact，并向模型提供行列规模和预览摘要
- [ ] **主动消息推送**：首次会话自动推送欢迎/帮助消息（见上方"主动消息推送"章节）
- [ ] **图片接收**：暂缓。若后续恢复，将作为低优先级能力处理，不作为当前默认接口
- [ ] **RDS/QS 文件支持**：后续考虑支持 `.rds` / `.qs` 的识别、转换和导入流程

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
