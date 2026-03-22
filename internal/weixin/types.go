package weixin

// iLink Bot protocol types.
// Ref: https://ilinkai.weixin.qq.com
// Ref: https://github.com/wong2/weixin-agent-sdk packages/sdk/src/

// BaseInfo is included in every API request per the iLink protocol.
type BaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

type QRCodeResponse struct {
	Ret              int    `json:"ret"`
	QRCode           string `json:"qrcode"`             // session identifier for polling
	QRCodeImgContent string `json:"qrcode_img_content"` // URL to encode as QR code
	Message          string `json:"message"`
}

type QRCodeStatusResponse struct {
	Ret         int    `json:"ret"`
	Status      string `json:"status"` // wait, scaned, confirmed, expired
	BotToken    string `json:"bot_token"`
	BaseURL     string `json:"baseurl"`
	ILinkBotID  string `json:"ilink_bot_id"`
	ILinkUserID string `json:"ilink_user_id"`
	Message     string `json:"message"`
}

type GetUpdatesRequest struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      BaseInfo `json:"base_info"`
}

type GetUpdatesResponse struct {
	Ret                int             `json:"ret"`
	Msgs               []WeixinMessage `json:"msgs"`
	GetUpdatesBuf      string          `json:"get_updates_buf"`
	LongPollingTimeout int             `json:"longpolling_timeout_ms"`
	ErrCode            int             `json:"errcode"`
}

type WeixinMessage struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id,omitempty"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ContextToken string        `json:"context_token"`
	ItemList     []MessageItem `json:"item_list"`
	GroupID      string        `json:"group_id,omitempty"`
}

type MessageItem struct {
	Type     int       `json:"type"`
	TextItem *TextItem `json:"text_item,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}

type SendMessageRequest struct {
	Msg      WeixinMessage `json:"msg"`
	BaseInfo BaseInfo      `json:"base_info"`
}

type SendMessageResponse struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode"`
	Message string `json:"message"`
}

type GetConfigRequest struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	ContextToken string   `json:"context_token,omitempty"`
	BaseInfo     BaseInfo `json:"base_info"`
}

type GetConfigResponse struct {
	Ret          int    `json:"ret"`
	TypingTicket string `json:"typing_ticket"`
}

type SendTypingRequest struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	TypingTicket string   `json:"typing_ticket"`
	Status       int      `json:"status"` // 1=typing, 2=cancel
	BaseInfo     BaseInfo `json:"base_info"`
}
