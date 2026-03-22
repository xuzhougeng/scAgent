package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const (
	DefaultBaseURL     = "https://ilinkai.weixin.qq.com"
	BotType            = "3"
	ChannelVersion     = "1.0.2"
	MessageTypeUser    = 1
	MessageTypeBot     = 2
	MessageStateFinish = 2
	ItemTypeText       = 1
	TypingStatusTyping = 1
)

// Client wraps the iLink Bot HTTP API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) SetToken(token string) {
	c.token = token
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func baseInfo() BaseInfo {
	return BaseInfo{ChannelVersion: ChannelVersion}
}

// generateClientID creates a unique message ID matching the TS SDK format.
func generateClientID() string {
	return fmt.Sprintf("openclaw-weixin-%d-%d", time.Now().UnixMilli(), rand.IntN(100000))
}

// GetQRCode starts a login flow and returns the QR code data.
func (c *Client) GetQRCode() (*QRCodeResponse, error) {
	url := fmt.Sprintf("%s/ilink/bot/get_bot_qrcode?bot_type=%s", c.baseURL, BotType)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result QRCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PollQRCodeStatus polls until the user scans the QR code or timeout.
func (c *Client) PollQRCodeStatus(qrcode string, timeout time.Duration) (*QRCodeStatusResponse, error) {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("%s/ilink/bot/get_qrcode_status?qrcode=%s", c.baseURL, qrcode)

	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		var result QRCodeStatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		if result.Status == "confirmed" {
			return &result, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("QR code login timed out after %v", timeout)
}

// GetUpdates long-polls for new messages. Blocks up to ~35s.
func (c *Client) GetUpdates(ctx context.Context, buf string) (*GetUpdatesResponse, error) {
	body := GetUpdatesRequest{
		GetUpdatesBuf: buf,
		BaseInfo:      baseInfo(),
	}
	var result GetUpdatesResponse
	if err := c.post(ctx, "/ilink/bot/getupdates", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendTextMessage sends a text reply to the given user.
func (c *Client) SendTextMessage(ctx context.Context, toUserID, text, contextToken string) error {
	msg := SendMessageRequest{
		Msg: WeixinMessage{
			ToUserID:     toUserID,
			ClientID:     generateClientID(),
			MessageType:  MessageTypeBot,
			MessageState: MessageStateFinish,
			ContextToken: contextToken,
			ItemList: []MessageItem{
				{Type: ItemTypeText, TextItem: &TextItem{Text: text}},
			},
		},
		BaseInfo: baseInfo(),
	}
	var result SendMessageResponse
	if err := c.post(ctx, "/ilink/bot/sendmessage", msg, &result); err != nil {
		return err
	}
	if result.Ret != 0 {
		return fmt.Errorf("sendmessage ret=%d errcode=%d: %s", result.Ret, result.ErrCode, result.Message)
	}
	return nil
}

// GetConfig retrieves the typing ticket for a user.
func (c *Client) GetConfig(ctx context.Context, userID, contextToken string) (*GetConfigResponse, error) {
	body := GetConfigRequest{
		ILinkUserID:  userID,
		ContextToken: contextToken,
		BaseInfo:     baseInfo(),
	}
	var result GetConfigResponse
	if err := c.post(ctx, "/ilink/bot/getconfig", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendTyping sends a "typing" indicator.
func (c *Client) SendTyping(ctx context.Context, userID, typingTicket string) error {
	body := SendTypingRequest{
		ILinkUserID:  userID,
		TypingTicket: typingTicket,
		Status:       TypingStatusTyping,
		BaseInfo:     baseInfo(),
	}
	var result map[string]any
	return c.post(ctx, "/ilink/bot/sendtyping", body, &result)
}

func (c *Client) post(ctx context.Context, path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("iLink API %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	uin := strconv.FormatUint(uint64(rand.Uint32()), 10)
	req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(uin)))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
