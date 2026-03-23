package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	qrterminal "github.com/mdp/qrterminal/v3"
	"scagent/internal/models"
	"scagent/internal/orchestrator"
)

const welcomeMessage = "你好！我是 scAgent，你的单细胞分析助手。\n\n" +
	"发送 .h5ad 文件开始分析，或直接输入问题。\n\n" +
	"常用命令:\n" +
	"/status — 查看当前会话\n" +
	"/l — 列出工作区\n" +
	"/h — 完整帮助\n\n" +
	"首次使用建议：发一个 h5ad 文件，我会自动评估数据质量并给出分析建议。"

// BridgeConfig holds all settings for the WeChat bridge.
type BridgeConfig struct {
	DataDir      string
	SessionLabel string
	JobTimeout   time.Duration
}

// pendingJob tracks a long-running job so the user can check back later.
type pendingJob struct {
	SessionID    string
	JobID        string
	StartedAt    time.Time
	ContextToken string // for auto-push when job completes
}

type replyPayload struct {
	Text   string
	Images []*models.Artifact
}

type inboundFileResult struct {
	InputArtifacts []*models.Artifact
	Messages       []string
}

// Bridge connects WeChat to the scAgent orchestrator.
type Bridge struct {
	client       *Client
	service      *orchestrator.Service
	config       BridgeConfig
	sessions     map[string]string      // wechat user id → scAgent session id
	pendingJobs  map[string]*pendingJob // wechat user id → pending job
	sessionsFile string
	mu           sync.Mutex
	typingTicket string
}

// NewBridge creates a bridge instance.
func NewBridge(client *Client, service *orchestrator.Service, config BridgeConfig) *Bridge {
	if config.SessionLabel == "" {
		config.SessionLabel = "WeChat"
	}
	if config.JobTimeout == 0 {
		config.JobTimeout = 5 * time.Minute
	}

	sessionsFile := filepath.Join(config.DataDir, "weixin-bridge", "sessions.json")
	b := &Bridge{
		client:       client,
		service:      service,
		config:       config,
		sessions:     make(map[string]string),
		pendingJobs:  make(map[string]*pendingJob),
		sessionsFile: sessionsFile,
	}
	b.loadSessions()
	return b
}

// Login performs QR code login interactively.
//
// Flow: GET get_bot_qrcode → render qrcode_img_content as terminal QR →
// poll get_qrcode_status until "confirmed" → persist bot_token.
// See: https://github.com/wong2/weixin-agent-sdk packages/sdk/src/auth/login-qr.ts
func (b *Bridge) Login() error {
	qr, err := b.client.GetQRCode()
	if err != nil {
		return fmt.Errorf("get QR code: %w", err)
	}
	if qr.QRCode == "" {
		return fmt.Errorf("no QR code returned: %s", qr.Message)
	}

	fmt.Println("\n请用微信扫描以下二维码：")

	// qrcode_img_content is the URL to encode as a scannable QR code
	if qr.QRCodeImgContent != "" {
		qrterminal.GenerateWithConfig(qr.QRCodeImgContent, qrterminal.Config{
			Level:     qrterminal.L,
			Writer:    os.Stdout,
			QuietZone: 1,
		})
		fmt.Printf("\n(如果无法扫描，请在浏览器打开: %s)\n", qr.QRCodeImgContent)
	} else {
		fmt.Printf("QR code session: %s\n", qr.QRCode)
	}

	fmt.Println("\n等待扫码...")

	status, err := b.client.PollQRCodeStatus(qr.QRCode, 8*time.Minute)
	if err != nil {
		return err
	}

	b.client.SetToken(status.BotToken)
	if status.BaseURL != "" {
		b.client = NewClient(status.BaseURL, status.BotToken)
	}

	// Persist account
	account := map[string]string{
		"token":      status.BotToken,
		"base_url":   status.BaseURL,
		"user_id":    status.ILinkUserID,
		"account_id": status.ILinkBotID,
	}
	if err := b.saveAccount(account); err != nil {
		log.Printf("[weixin] warning: failed to save account: %v", err)
	}

	log.Printf("[weixin] login succeeded, account=%s", status.ILinkBotID)
	return nil
}

// LoadAccount loads a previously saved account. Returns false if none exists.
func (b *Bridge) LoadAccount() bool {
	path := filepath.Join(b.config.DataDir, "weixin-bridge", "account.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var account map[string]string
	if err := json.Unmarshal(data, &account); err != nil {
		return false
	}
	token := account["token"]
	if token == "" {
		return false
	}
	baseURL := account["base_url"]
	if baseURL == "" {
		baseURL = b.client.BaseURL()
	}
	b.client = NewClient(baseURL, token)
	log.Printf("[weixin] loaded account %s", account["account_id"])
	return true
}

// Logout removes saved account and session data.
func (b *Bridge) Logout() error {
	dir := filepath.Join(b.config.DataDir, "weixin-bridge")
	for _, name := range []string{"account.json", "sessions.json", "sync_buf"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	log.Printf("[weixin] logged out, credentials removed")
	return nil
}

// Run starts the long-polling message loop. Blocks until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	log.Printf("[weixin] bridge started, polling for messages")

	var updatesBuf string
	bufPath := filepath.Join(b.config.DataDir, "weixin-bridge", "sync_buf")
	if data, err := os.ReadFile(bufPath); err == nil {
		updatesBuf = string(data)
		log.Printf("[weixin] restored sync buf from disk")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := b.client.GetUpdates(ctx, updatesBuf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("[weixin] getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if resp.ErrCode == -14 {
			log.Printf("[weixin] session expired (errcode -14), need re-login")
			return fmt.Errorf("weixin session expired, please re-login")
		}

		if resp.GetUpdatesBuf != "" {
			updatesBuf = resp.GetUpdatesBuf
			// Persist buf for restart recovery
			dir := filepath.Dir(bufPath)
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(bufPath, []byte(updatesBuf), 0o644)
		}

		for _, msg := range resp.Msgs {
			if msg.MessageType != MessageTypeUser {
				continue
			}
			if !hasProcessableInput(msg) {
				continue
			}

			go b.handleMessage(ctx, msg)
		}
	}
}

func (b *Bridge) handleMessage(ctx context.Context, msg WeixinMessage) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[weixin] panic handling message from %s: %v", msg.FromUserID, r)
		}
	}()

	fromUserID := msg.FromUserID
	contextToken := msg.ContextToken
	text := extractText(msg)
	logText := text
	if strings.TrimSpace(logText) == "" && len(extractFileItems(msg)) > 0 {
		logText = "[file]"
	}
	log.Printf("[weixin] %s: %s", fromUserID, truncate(logText, 80))

	// Handle slash commands — normalize full-width "／" (Chinese IME) to ASCII "/"
	if strings.HasPrefix(text, "／") {
		text = "/" + strings.TrimPrefix(text, "／")
	}
	if strings.HasPrefix(text, "/") {
		reply := b.handleSlashCommand(ctx, fromUserID, text)
		if reply != "" {
			if err := b.client.SendTextMessage(ctx, fromUserID, reply, contextToken); err != nil {
				log.Printf("[weixin] failed to send slash reply to %s: %v", fromUserID, err)
			} else {
				log.Printf("[weixin] slash reply sent to %s: %s", fromUserID, truncate(reply, 40))
			}
			return
		}
		// Not a recognized command — fall through to normal processing
	}

	// Ping test
	if strings.EqualFold(text, "hi") {
		_ = b.client.SendTextMessage(ctx, fromUserID, "hello, i'm back", contextToken)
		return
	}

	// Check if user has a pending long-running job
	if reply, handled := b.checkPendingJob(fromUserID); handled {
		_ = b.sendReply(ctx, fromUserID, contextToken, reply)
		return
	}

	// Fetch typing ticket on demand (per-user) and send indicator
	if b.typingTicket == "" {
		if cfg, err := b.client.GetConfig(ctx, fromUserID, contextToken); err == nil && cfg.TypingTicket != "" {
			b.typingTicket = cfg.TypingTicket
		}
	}
	if b.typingTicket != "" {
		_ = b.client.SendTyping(ctx, fromUserID, b.typingTicket)
	}

	// Resolve or create scAgent session
	sessionID, isNewSession, err := b.resolveSession(ctx, fromUserID)
	if err != nil {
		log.Printf("[weixin] session resolve error: %v", err)
		_ = b.client.SendTextMessage(ctx, fromUserID, "系统错误，请稍后重试", contextToken)
		return
	}

	// 首次会话：先推送欢迎消息
	if isNewSession {
		if err := b.client.SendTextMessage(ctx, fromUserID, welcomeMessage, contextToken); err != nil {
			log.Printf("[weixin] welcome message failed for %s: %v", fromUserID, err)
		} else {
			log.Printf("[weixin] welcome message sent to %s", fromUserID)
		}
		time.Sleep(200 * time.Millisecond)
	}

	fileResult, err := b.processInboundFiles(ctx, sessionID, msg)
	if err != nil {
		log.Printf("[weixin] inbound file handling failed: %v", err)
		if strings.TrimSpace(text) == "" {
			_ = b.client.SendTextMessage(ctx, fromUserID, "文件接收失败，请稍后重试。", contextToken)
			return
		}
	}
	inputArtifacts := fileResult.InputArtifacts
	if strings.TrimSpace(text) == "" {
		replyText := strings.Join(fileResult.Messages, "\n")
		replyText = strings.TrimSpace(replyText)
		if replyText == "" {
			replyText = "已收到文件。请告诉我如何使用它。"
		}
		_ = b.client.SendTextMessage(ctx, fromUserID, replyText, contextToken)
		return
	}

	// Submit message
	job, snapshot, err := b.service.SubmitMessageWithArtifacts(ctx, sessionID, text, inputArtifacts)
	if err != nil {
		// Session may be stale — retry with new session
		log.Printf("[weixin] submit failed for %s, recreating: %v", sessionID, err)
		b.deleteSession(fromUserID)
		sessionID, _, err = b.resolveSession(ctx, fromUserID)
		if err != nil {
			_ = b.client.SendTextMessage(ctx, fromUserID, "系统错误，请稍后重试", contextToken)
			return
		}
		fileResult, _ = b.processInboundFiles(ctx, sessionID, msg)
		inputArtifacts = fileResult.InputArtifacts
		job, snapshot, err = b.service.SubmitMessageWithArtifacts(ctx, sessionID, text, inputArtifacts)
		if err != nil {
			log.Printf("[weixin] submit retry failed: %v", err)
			_ = b.client.SendTextMessage(ctx, fromUserID, fmt.Sprintf("提交失败: %v", err), contextToken)
			return
		}
	}

	if job == nil {
		reply := replyPayload{Text: latestAssistantMessage(snapshot)}
		if reply.Text == "" {
			reply.Text = "已收到请求。"
		}
		if err := b.sendReply(ctx, fromUserID, contextToken, reply); err != nil {
			log.Printf("[weixin] send direct reply error to %s: %v", fromUserID, err)
		}
		return
	}

	// Wait for job completion — if it takes >30s, notify user and track as pending
	reply, done := b.waitForJobWithTimeout(ctx, sessionID, job.ID, 30*time.Second)
	if !done {
		// Job still running — save as pending and start background watcher
		pj := &pendingJob{
			SessionID:    sessionID,
			JobID:        job.ID,
			StartedAt:    time.Now(),
			ContextToken: contextToken,
		}
		b.mu.Lock()
		b.pendingJobs[fromUserID] = pj
		b.mu.Unlock()
		reply = replyPayload{Text: "任务运行时间较长，完成后会自动发送结果。"}
		go b.watchPendingJob(fromUserID, pj)
	}
	if err := b.sendReply(ctx, fromUserID, contextToken, reply); err != nil {
		log.Printf("[weixin] send reply error to %s: %v", fromUserID, err)
	} else {
		log.Printf("[weixin] replied to %s (%d chars, %d images)", fromUserID, len(reply.Text), len(reply.Images))
	}
}

func latestAssistantMessage(snapshot *models.SessionSnapshot) string {
	if snapshot == nil {
		return ""
	}
	for index := len(snapshot.Messages) - 1; index >= 0; index-- {
		message := snapshot.Messages[index]
		if message != nil && message.Role == models.MessageAssistant {
			return strings.TrimSpace(message.Content)
		}
	}
	return ""
}

// handleSlashCommand processes /commands from WeChat. Returns reply text, or "" if not a recognized command.
func (b *Bridge) handleSlashCommand(ctx context.Context, fromUserID, text string) string {
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help", "/h":
		return "可用命令:\n" +
			"/status — 查看当前会话状态\n" +
			"/l — 列出所有工作区\n" +
			"/s <序号> — 切换工作区（如 /s 1）\n" +
			"/lc — 列出当前工作区的对话\n" +
			"/sc <序号> — 切换对话（如 /sc 1）\n" +
			"/nc — 在当前工作区新建对话\n" +
			"/new — 创建新工作区+会话\n" +
			"/reset — 重置会话映射\n" +
			"/h — 显示此帮助"

	case "/status":
		b.mu.Lock()
		sessionID := b.sessions[fromUserID]
		b.mu.Unlock()
		if sessionID == "" {
			return "当前无活跃会话。发送任意消息开始。"
		}
		snapshot, err := b.service.GetSnapshot(sessionID)
		if err != nil {
			return fmt.Sprintf("会话 %s（已失效）", sessionID)
		}
		ws := snapshot.Workspace
		if ws == nil {
			return fmt.Sprintf("会话: %s\n标签: %s", snapshot.Session.ID, snapshot.Session.Label)
		}
		return fmt.Sprintf("会话: %s\n工作区: %s (%s)\n对象数: %d\n消息数: %d",
			snapshot.Session.ID, ws.ID, ws.Label,
			len(snapshot.Objects), len(snapshot.Messages))

	case "/workspaces", "/list", "/l":
		wsList := b.service.ListWorkspaces()
		if wsList == nil || len(wsList.Workspaces) == 0 {
			return "暂无工作区。发送任意消息将自动创建。"
		}
		var sb strings.Builder
		sb.WriteString("工作区列表:\n")
		for i, ws := range wsList.Workspaces {
			marker := "  "
			// Check if current session belongs to this workspace
			b.mu.Lock()
			sessionID := b.sessions[fromUserID]
			b.mu.Unlock()
			if sessionID != "" {
				if snap, err := b.service.GetSnapshot(sessionID); err == nil && snap.Workspace != nil && snap.Workspace.ID == ws.ID {
					marker = "→ "
				}
			}
			label := ws.Label
			if label == "" {
				label = ws.ID
			}
			sb.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, label))
		}
		sb.WriteString("\n用 /s <序号> 切换，如 /s 1")
		return sb.String()

	case "/switch", "/s":
		if len(parts) < 2 {
			return "用法: /s <序号>\n用 /l 查看工作区列表"
		}
		targetWS := parts[1]
		// Support switching by index number (e.g., /s 1)
		if idx, err := strconv.Atoi(targetWS); err == nil {
			wsList := b.service.ListWorkspaces()
			if wsList == nil || idx < 1 || idx > len(wsList.Workspaces) {
				return fmt.Sprintf("无效序号 %d，用 /l 查看列表", idx)
			}
			targetWS = wsList.Workspaces[idx-1].ID
		}
		label := fmt.Sprintf("%s-%s", b.config.SessionLabel, truncate(fromUserID, 12))
		snapshot, err := b.service.CreateConversation(ctx, targetWS, label)
		if err != nil {
			return fmt.Sprintf("切换失败: %v", err)
		}
		b.setSession(fromUserID, snapshot.Session.ID)
		wsLabel := ""
		if snapshot.Workspace != nil {
			wsLabel = snapshot.Workspace.Label
		}
		return fmt.Sprintf("已切换到工作区 %s (%s)\n新会话: %s", targetWS, wsLabel, snapshot.Session.ID)

	case "/list-chat", "/lc":
		b.mu.Lock()
		sessionID := b.sessions[fromUserID]
		b.mu.Unlock()
		if sessionID == "" {
			return "当前无活跃会话。发送任意消息开始。"
		}
		snap, err := b.service.GetSnapshot(sessionID)
		if err != nil || snap.Workspace == nil {
			return "无法获取当前工作区信息"
		}
		wsSnap, err := b.service.GetWorkspaceSnapshot(snap.Workspace.ID)
		if err != nil || len(wsSnap.Conversations) == 0 {
			return "当前工作区暂无对话"
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("工作区 %s 的对话:\n", snap.Workspace.Label))
		for i, conv := range wsSnap.Conversations {
			marker := "  "
			if conv.ID == sessionID {
				marker = "→ "
			}
			label := conv.Label
			if label == "" {
				label = conv.ID
			}
			sb.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, label))
		}
		sb.WriteString("\n用 /sc <序号> 切换，如 /sc 1")
		return sb.String()

	case "/switch-chat", "/sc":
		if len(parts) < 2 {
			return "用法: /sc <序号>\n用 /lc 查看对话列表"
		}
		b.mu.Lock()
		sessionID := b.sessions[fromUserID]
		b.mu.Unlock()
		if sessionID == "" {
			return "当前无活跃会话。发送任意消息开始。"
		}
		snap, err := b.service.GetSnapshot(sessionID)
		if err != nil || snap.Workspace == nil {
			return "无法获取当前工作区信息"
		}
		wsSnap, err := b.service.GetWorkspaceSnapshot(snap.Workspace.ID)
		if err != nil {
			return "无法获取工作区对话列表"
		}
		idx, err := strconv.Atoi(parts[1])
		if err != nil || idx < 1 || idx > len(wsSnap.Conversations) {
			return fmt.Sprintf("无效序号，用 /lc 查看列表（共 %d 个对话）", len(wsSnap.Conversations))
		}
		target := wsSnap.Conversations[idx-1]
		b.setSession(fromUserID, target.ID)
		label := target.Label
		if label == "" {
			label = target.ID
		}
		return fmt.Sprintf("已切换到对话: %s", label)

	case "/new-chat", "/nc":
		b.mu.Lock()
		sessionID := b.sessions[fromUserID]
		b.mu.Unlock()
		if sessionID == "" {
			return "当前无活跃会话。发送任意消息或 /new 创建工作区。"
		}
		snap, err := b.service.GetSnapshot(sessionID)
		if err != nil || snap.Workspace == nil {
			return "无法获取当前工作区信息"
		}
		label := fmt.Sprintf("%s-%s", b.config.SessionLabel, truncate(fromUserID, 12))
		newSnap, err := b.service.CreateConversation(ctx, snap.Workspace.ID, label)
		if err != nil {
			return fmt.Sprintf("创建对话失败: %v", err)
		}
		b.setSession(fromUserID, newSnap.Session.ID)
		return fmt.Sprintf("已在工作区 %s 新建对话\n会话: %s", snap.Workspace.Label, newSnap.Session.ID)

	case "/new":
		label := fmt.Sprintf("%s-%s", b.config.SessionLabel, truncate(fromUserID, 12))
		snapshot, err := b.service.CreateSession(ctx, label, true)
		if err != nil {
			return fmt.Sprintf("创建失败: %v", err)
		}
		b.setSession(fromUserID, snapshot.Session.ID)
		return fmt.Sprintf("已创建新工作区\n会话: %s", snapshot.Session.ID)

	case "/reset":
		b.deleteSession(fromUserID)
		return "会话已重置。下次发送消息将自动加入最近的工作区。"

	default:
		return "" // Not a recognized command
	}
}

// waitForJobWithTimeout waits for a job to complete. If the job finishes within
// the given timeout, it returns the result and done=true. If the timeout fires
// first, it returns ("", false) so the caller can track it as a pending job.
func (b *Bridge) waitForJobWithTimeout(ctx context.Context, sessionID, jobID string, timeout time.Duration) (replyPayload, bool) {
	events, cancel := b.service.Subscribe(sessionID)
	defer cancel()

	timer := time.After(timeout)
	hardTimeout := time.After(b.config.JobTimeout)

	for {
		select {
		case <-ctx.Done():
			return replyPayload{Text: "系统已停止"}, true
		case <-hardTimeout:
			return replyPayload{Text: "分析超时，请稍后重试"}, true
		case <-timer:
			return replyPayload{}, false
		case event, ok := <-events:
			if !ok {
				return replyPayload{Text: "事件流中断"}, true
			}
			if event.Type != "session_updated" {
				continue
			}

			snapshot, ok := event.Data.(*models.SessionSnapshot)
			if !ok {
				continue
			}

			for _, job := range snapshot.Jobs {
				if job.ID != jobID {
					continue
				}
				if job.Status == models.JobSucceeded {
					return b.buildJobReply(snapshot, jobID), true
				}
				if job.Status == models.JobFailed {
					if job.Error != "" {
						return replyPayload{Text: fmt.Sprintf("分析失败: %s", job.Error)}, true
					}
					return replyPayload{Text: "分析失败"}, true
				}
			}
		}
	}
}

// checkPendingJob checks whether the user has a pending long-running job.
// If the job is finished, it returns the result and clears the pending state.
// If the job is still running, it tells the user to wait.
// Returns "" if there is no pending job (normal message flow should continue).
func (b *Bridge) checkPendingJob(fromUserID string) (replyPayload, bool) {
	b.mu.Lock()
	pj := b.pendingJobs[fromUserID]
	b.mu.Unlock()
	if pj == nil {
		return replyPayload{}, false
	}

	// Check job status from current snapshot
	snapshot, err := b.service.GetSnapshot(pj.SessionID)
	if err != nil {
		// Session gone — clear pending
		b.mu.Lock()
		delete(b.pendingJobs, fromUserID)
		b.mu.Unlock()
		return replyPayload{}, false
	}

	for _, job := range snapshot.Jobs {
		if job.ID != pj.JobID {
			continue
		}
		if job.Status == models.JobSucceeded {
			b.mu.Lock()
			delete(b.pendingJobs, fromUserID)
			b.mu.Unlock()
			return b.buildJobReply(snapshot, pj.JobID), true
		}
		if job.Status == models.JobFailed {
			b.mu.Lock()
			delete(b.pendingJobs, fromUserID)
			b.mu.Unlock()
			if job.Error != "" {
				return replyPayload{Text: fmt.Sprintf("分析失败: %s", job.Error)}, true
			}
			return replyPayload{Text: "分析失败"}, true
		}
		// Still running
		elapsed := time.Since(pj.StartedAt).Truncate(time.Second)
		return replyPayload{
			Text: fmt.Sprintf("任务仍在运行中（已耗时 %s），请再等一分钟后发消息查看。", elapsed),
		}, true
	}

	// Job not found in snapshot — clear pending
	b.mu.Lock()
	delete(b.pendingJobs, fromUserID)
	b.mu.Unlock()
	return replyPayload{}, false
}

// watchPendingJob subscribes to session events and auto-pushes the result
// when the pending job completes, so the user doesn't have to ask for it.
func (b *Bridge) watchPendingJob(userID string, pj *pendingJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[weixin] panic in watchPendingJob for %s: %v", userID, r)
		}
	}()

	events, cancel := b.service.Subscribe(pj.SessionID)
	defer cancel()

	hardTimeout := time.After(b.config.JobTimeout)
	for {
		select {
		case <-hardTimeout:
			b.clearAndPush(userID, pj, replyPayload{Text: "任务超时，请重新提交。"})
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Type != "session_updated" {
				continue
			}
			snapshot, ok := event.Data.(*models.SessionSnapshot)
			if !ok {
				continue
			}
			for _, job := range snapshot.Jobs {
				if job.ID != pj.JobID {
					continue
				}
				if job.Status == models.JobSucceeded {
					b.clearAndPush(userID, pj, b.buildJobReply(snapshot, pj.JobID))
					return
				}
				if job.Status == models.JobFailed {
					msg := "分析失败"
					if job.Error != "" {
						msg = fmt.Sprintf("分析失败: %s", job.Error)
					}
					b.clearAndPush(userID, pj, replyPayload{Text: msg})
					return
				}
			}
		}
	}
}

// clearAndPush removes the pending job and sends the result to the user.
func (b *Bridge) clearAndPush(userID string, pj *pendingJob, reply replyPayload) {
	b.mu.Lock()
	// Only clear if it's still the same pending job (user may have started a new one)
	if current := b.pendingJobs[userID]; current == pj {
		delete(b.pendingJobs, userID)
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.sendReply(ctx, userID, pj.ContextToken, reply); err != nil {
		log.Printf("[weixin] auto-push failed for %s: %v", userID, err)
	} else {
		log.Printf("[weixin] auto-pushed result to %s (%d chars, %d images)", userID, len(reply.Text), len(reply.Images))
	}
}

func (b *Bridge) buildJobReply(snapshot *models.SessionSnapshot, jobID string) replyPayload {
	reply := replyPayload{
		Images: jobImageArtifacts(snapshot, jobID),
	}

	// Find assistant message for this job
	for i := len(snapshot.Messages) - 1; i >= 0; i-- {
		msg := snapshot.Messages[i]
		if msg.JobID == jobID && msg.Role == models.MessageAssistant {
			reply.Text = strings.TrimSpace(msg.Content)
			return finalizeReplyPayload(reply)
		}
	}

	// Fallback: find job summary
	for _, job := range snapshot.Jobs {
		if job.ID == jobID && job.Summary != "" {
			reply.Text = strings.TrimSpace(job.Summary)
			return finalizeReplyPayload(reply)
		}
	}
	reply.Text = "完成"
	return finalizeReplyPayload(reply)
}

func finalizeReplyPayload(reply replyPayload) replyPayload {
	reply.Text = strings.TrimSpace(reply.Text)
	if len(reply.Images) > 0 {
		if reply.Text == "" {
			reply.Text = "已生成图表。"
		}
		reply.Text = strings.TrimSpace(reply.Text + fmt.Sprintf("\n\n已附上 %d 张图。", len(reply.Images)))
	}
	if reply.Text == "" {
		reply.Text = "已收到请求。"
	}
	return reply
}

func jobImageArtifacts(snapshot *models.SessionSnapshot, jobID string) []*models.Artifact {
	if snapshot == nil {
		return nil
	}

	var images []*models.Artifact
	for _, artifact := range snapshot.Artifacts {
		if artifact == nil || artifact.JobID != jobID || artifact.Kind != models.ArtifactPlot {
			continue
		}
		if !isImageArtifact(artifact) {
			continue
		}
		images = append(images, artifact)
	}
	return images
}

func isImageArtifact(artifact *models.Artifact) bool {
	if artifact == nil || strings.TrimSpace(artifact.Path) == "" {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(artifact.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(artifact.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func (b *Bridge) sendReply(ctx context.Context, toUserID, contextToken string, reply replyPayload) error {
	reply = finalizeReplyPayload(reply)
	if reply.Text != "" {
		if err := b.client.SendTextMessage(ctx, toUserID, reply.Text, contextToken); err != nil {
			return err
		}
	}

	var failed []string
	for _, artifact := range reply.Images {
		if artifact == nil || !isImageArtifact(artifact) {
			continue
		}
		if err := b.client.SendImageFile(ctx, toUserID, artifact.Path, contextToken); err != nil {
			title := strings.TrimSpace(artifact.Title)
			if title == "" {
				title = filepath.Base(artifact.Path)
			}
			failed = append(failed, title)
			log.Printf("[weixin] image send failed for %s: %v", artifact.Path, err)
		}
	}

	if len(failed) > 0 {
		notice := fmt.Sprintf("以下图片发送失败：%s", strings.Join(failed, "、"))
		if err := b.client.SendTextMessage(ctx, toUserID, notice, contextToken); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bridge) resolveSession(ctx context.Context, weixinUserID string) (sessionID string, isNew bool, err error) {
	b.mu.Lock()
	cached, ok := b.sessions[weixinUserID]
	b.mu.Unlock()
	if ok {
		return cached, false, nil
	}

	label := fmt.Sprintf("%s-%s", b.config.SessionLabel, truncate(weixinUserID, 12))

	// Try to reuse the most recent workspace
	wsList := b.service.ListWorkspaces()
	if wsList != nil && len(wsList.Workspaces) > 0 {
		workspaces := make([]*models.Workspace, len(wsList.Workspaces))
		copy(workspaces, wsList.Workspaces)
		sort.Slice(workspaces, func(i, j int) bool {
			return workspaces[i].LastAccessedAt.After(workspaces[j].LastAccessedAt)
		})

		ws := workspaces[0]
		log.Printf("[weixin] %s → joining workspace %s (%q)", weixinUserID, ws.ID, ws.Label)
		snapshot, err := b.service.CreateConversation(ctx, ws.ID, label)
		if err == nil {
			sid := snapshot.Session.ID
			b.setSession(weixinUserID, sid)
			return sid, true, nil
		}
		log.Printf("[weixin] create conversation in %s failed: %v, creating new", ws.ID, err)
	}

	// Create brand new session+workspace
	log.Printf("[weixin] %s → creating new session %q", weixinUserID, label)
	snapshot, err := b.service.CreateSession(ctx, label, true)
	if err != nil {
		return "", false, err
	}
	sid := snapshot.Session.ID
	b.setSession(weixinUserID, sid)
	return sid, true, nil
}

func (b *Bridge) setSession(weixinUserID, sessionID string) {
	b.mu.Lock()
	b.sessions[weixinUserID] = sessionID
	b.mu.Unlock()
	b.saveSessions()
}

func (b *Bridge) deleteSession(weixinUserID string) {
	b.mu.Lock()
	delete(b.sessions, weixinUserID)
	b.mu.Unlock()
	b.saveSessions()
}

func (b *Bridge) loadSessions() {
	data, err := os.ReadFile(b.sessionsFile)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &b.sessions)
}

func (b *Bridge) saveSessions() {
	b.mu.Lock()
	data, _ := json.MarshalIndent(b.sessions, "", "  ")
	b.mu.Unlock()

	dir := filepath.Dir(b.sessionsFile)
	_ = os.MkdirAll(dir, 0o755)
	tmp := b.sessionsFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, b.sessionsFile)
}

func (b *Bridge) saveAccount(account map[string]string) error {
	dir := filepath.Join(b.config.DataDir, "weixin-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "account.json"), data, 0o644)
}

func hasProcessableInput(msg WeixinMessage) bool {
	return strings.TrimSpace(extractText(msg)) != "" || len(extractFileItems(msg)) > 0
}

func extractImageItems(msg WeixinMessage) []MessageItem {
	var items []MessageItem
	for _, item := range msg.ItemList {
		if item.Type == ItemTypeImage && item.ImageItem != nil && item.ImageItem.Media != nil && strings.TrimSpace(item.ImageItem.Media.EncryptQueryParam) != "" {
			items = append(items, item)
		}
	}
	return items
}

func extractFileItems(msg WeixinMessage) []MessageItem {
	var items []MessageItem
	for _, item := range msg.ItemList {
		if item.Type == 4 && item.FileItem != nil && item.FileItem.Media != nil && strings.TrimSpace(item.FileItem.Media.EncryptQueryParam) != "" {
			items = append(items, item)
		}
	}
	return items
}

func (b *Bridge) processInboundFiles(ctx context.Context, sessionID string, msg WeixinMessage) (*inboundFileResult, error) {
	fileItems := extractFileItems(msg)
	if len(fileItems) == 0 {
		return &inboundFileResult{}, nil
	}

	result := &inboundFileResult{
		InputArtifacts: make([]*models.Artifact, 0, len(fileItems)),
		Messages:       make([]string, 0, len(fileItems)),
	}

	for _, item := range fileItems {
		payload, filename, contentType, err := b.downloadInboundFile(ctx, item)
		if err != nil {
			return result, err
		}
		switch inboundFileKind(filename) {
		case "h5ad":
			summary := fmt.Sprintf("h5ad 文件 %s 已登记为后续数据转换/导入锚点；当前不会直接载入会话，需要在后续主流程中显式转换或导入。", filename)
			artifact, _, err := b.service.RegisterExternalArtifact(
				ctx,
				sessionID,
				models.ArtifactFile,
				filename,
				normalizedInboundContentType(filename, contentType),
				"微信文件："+filename,
				summary,
				bytes.NewReader(payload),
			)
			if err != nil {
				return result, err
			}
			result.InputArtifacts = append(result.InputArtifacts, artifact)
			result.Messages = append(result.Messages, fmt.Sprintf("已收到 h5ad 文件 %s。已登记为后续数据转换/导入锚点，当前不会直接载入会话。", filename))
		case "csv", "tsv":
			summary := summarizeDelimitedFile(filename, payload)
			artifact, _, err := b.service.RegisterExternalArtifact(
				ctx,
				sessionID,
				models.ArtifactFile,
				filename,
				normalizedInboundContentType(filename, contentType),
				"微信文件："+filename,
				summary,
				bytes.NewReader(payload),
			)
			if err != nil {
				return result, err
			}
			result.InputArtifacts = append(result.InputArtifacts, artifact)
			result.Messages = append(result.Messages, fmt.Sprintf("已收到表格文件 %s。%s", filename, summary))
		default:
			result.Messages = append(result.Messages, fmt.Sprintf("暂不支持文件 %s。当前微信文件接收仅支持 h5ad、csv、tsv。", filename))
		}
	}
	return result, nil
}

func (b *Bridge) downloadInboundFile(ctx context.Context, item MessageItem) ([]byte, string, string, error) {
	if item.FileItem == nil || item.FileItem.Media == nil {
		return nil, "", "", fmt.Errorf("missing file item media")
	}
	aesKey := strings.TrimSpace(item.FileItem.Media.AESKey)
	if aesKey == "" {
		return nil, "", "", fmt.Errorf("missing file aes key")
	}
	payload, err := b.client.DownloadAndDecryptCDNBuffer(ctx, item.FileItem.Media.EncryptQueryParam, aesKey)
	if err != nil {
		return nil, "", "", err
	}
	filename := strings.TrimSpace(item.FileItem.FileName)
	if filename == "" {
		filename = fmt.Sprintf("weixin_file_%d.bin", time.Now().UnixMilli())
	}
	contentType := http.DetectContentType(payload)
	return payload, filename, contentType, nil
}

func imageExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

func inboundFileKind(filename string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".h5ad", ".ha5d":
		return "h5ad"
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	default:
		return ""
	}
}

func normalizedInboundContentType(filename, detected string) string {
	switch inboundFileKind(filename) {
	case "h5ad":
		return "application/x-h5ad"
	case "csv":
		return "text/csv"
	case "tsv":
		return "text/tab-separated-values"
	default:
		return detected
	}
}

func latestSystemOrAssistantMessage(snapshot *models.SessionSnapshot, fallback string) string {
	if snapshot == nil {
		return fallback
	}
	for index := len(snapshot.Messages) - 1; index >= 0; index-- {
		message := snapshot.Messages[index]
		if message == nil {
			continue
		}
		if message.Role == models.MessageSystem || message.Role == models.MessageAssistant {
			content := strings.TrimSpace(message.Content)
			if content != "" {
				return content
			}
		}
	}
	return fallback
}

func extractText(msg WeixinMessage) string {
	for _, item := range msg.ItemList {
		if item.Type == ItemTypeText && item.TextItem != nil {
			return strings.TrimSpace(item.TextItem.Text)
		}
		// 语音消息：微信服务端转写的文字在 voice_item.text
		if item.Type == ItemTypeVoice && item.VoiceItem != nil && item.VoiceItem.Text != "" {
			return strings.TrimSpace(item.VoiceItem.Text)
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
