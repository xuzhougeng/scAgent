# 微信接入指南

## 概述

scAgent 支持通过微信直接对话，用户在微信中发送分析请求，系统返回文字结果和图表。

桥接层基于 [weixin-agent-sdk](https://github.com/wong2/weixin-agent-sdk) 实现，是一个独立的 Node.js 进程，通过 HTTP/SSE 与 scAgent 控制平面通信。

## 前置要求

- Node.js >= 22
- pnpm

## 快速开始

### 1. 安装依赖

```bash
cd im/weixin
pnpm install
```

### 2. 微信扫码登录

```bash
make weixin-login
```

终端会打印一个二维码，用微信扫码完成登录。登录凭证保存在 `~/.openclaw/`，后续启动无需重复扫码（会话过期后需重新登录）。

### 3. 启动

有两种方式启动微信桥接：

**方式 A：独立启动（推荐调试时使用）**

先在一个终端启动 scAgent：

```bash
make dev
```

再在另一个终端启动桥接：

```bash
cd im/weixin
pnpm run start
```

**方式 B：随 make dev 一起启动**

在 `.env` 中设置：

```
WEIXIN_BRIDGE_ENABLED=1
```

然后正常启动：

```bash
make dev
```

桥接进程会在控制平面健康后自动启动。

**方式 C：专用 make 命令**

```bash
make weixin
```

等同于 `WEIXIN_BRIDGE_ENABLED=1 ./start.sh`。

## 配置项

所有配置通过环境变量或 `.env` 文件设置：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WEIXIN_BRIDGE_ENABLED` | `0` | 是否随 start.sh 启动桥接 |
| `SCAGENT_BASE_URL` | `http://127.0.0.1:8080` | scAgent 控制平面地址 |
| `WEIXIN_BRIDGE_TIMEOUT_MS` | `300000` | 单次分析任务超时（毫秒），默认 5 分钟 |
| `WEIXIN_BRIDGE_SESSION_LABEL` | `WeChat` | 新建 session 的标签前缀 |

## 工作原理

```
微信用户 ←→ weixin-agent-sdk（长轮询）
                  ↓
            ScAgentBridge
                  ↓
        POST /api/messages → 提交用户消息
        GET /api/sessions/{id}/events → SSE 等待 job 完成
                  ↓
            scAgent Go 控制平面（:8080）
```

1. 每个微信联系人（`conversationId`）映射到一个 scAgent session
2. 用户发送文字 → 桥接 POST 到 `/api/messages`
3. 桥接通过 SSE 监听 job 状态变化
4. job 完成后，提取 assistant 回复和 plot 产物
5. 文字回复 + 第一张图表发回微信

## 会话映射

微信联系人与 scAgent session 的映射关系保存在：

```
data/weixin-bridge/sessions.json
```

如需重置某个用户的会话，可手动编辑或删除此文件。

## 支持的消息类型

### 接收（微信 → scAgent）

当前仅处理文本消息。图片、语音、文件等媒体类型会被忽略。

### 发送（scAgent → 微信）

- 文字回复：分析结果摘要
- 图片回复：plot 产物（UMAP、marker 图等）
- 如果一次分析产生多张图，只发送第一张，文字中会提示总数

## 常见问题

**Q: 扫码后提示登录失败？**
A: 确保微信账号正常，重新运行 `make weixin-login`。

**Q: 发消息后长时间无回复？**
A: 检查 scAgent 是否正常运行（`curl http://127.0.0.1:8080/healthz`）。部分分析任务可能耗时较长，默认超时 5 分钟。

**Q: 如何同时使用 Web 界面和微信？**
A: 两者完全独立。微信用户会创建独立的 session，不影响 Web 界面的操作。

**Q: 会话过期怎么办？**
A: SDK 会自动尝试重连。如果持续断线，重新运行 `make weixin-login` 扫码登录。
