package weixin

import (
	"bytes"
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
func (c *Client) GetUpdates(buf string) (*GetUpdatesResponse, error) {
	body := GetUpdatesRequest{
		GetUpdatesBuf: buf,
		BaseInfo:      GetUpdatesBase{ChannelVersion: ChannelVersion},
	}
	var result GetUpdatesResponse
	if err := c.post("/ilink/bot/getupdates", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendTextMessage sends a text reply to the given user.
func (c *Client) SendTextMessage(toUserID, text, contextToken string) error {
	msg := SendMessageRequest{
		Msg: WeixinMessage{
			ToUserID:     toUserID,
			MessageType:  MessageTypeBot,
			MessageState: MessageStateFinish,
			ContextToken: contextToken,
			ItemList: []MessageItem{
				{Type: ItemTypeText, TextItem: &TextItem{Text: text}},
			},
		},
	}
	var result SendMessageResponse
	if err := c.post("/ilink/bot/sendmessage", msg, &result); err != nil {
		return err
	}
	if result.Ret != 0 {
		return fmt.Errorf("sendmessage ret=%d errcode=%d: %s", result.Ret, result.ErrCode, result.Message)
	}
	return nil
}

// GetConfig retrieves the typing ticket.
func (c *Client) GetConfig() (*GetConfigResponse, error) {
	var result GetConfigResponse
	if err := c.post("/ilink/bot/getconfig", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendTyping sends a "typing" indicator.
func (c *Client) SendTyping(toUserID, typingTicket, contextToken string) error {
	body := SendTypingRequest{
		ToUserID:     toUserID,
		TypingTicket: typingTicket,
		ContextToken: contextToken,
	}
	var result map[string]any
	return c.post("/ilink/bot/sendtyping", body, &result)
}

func (c *Client) post(path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
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
