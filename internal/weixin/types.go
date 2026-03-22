package weixin

// iLink Bot protocol types.
// Ref: https://ilinkai.weixin.qq.com

type QRCodeResponse struct {
	Ret            int    `json:"ret"`
	QRCode         string `json:"qrcode"`
	QRCodeURL      string `json:"qrcode_url"`
	QRCodeImgBytes string `json:"qrcode_img_content"`
	Message        string `json:"message"`
}

type QRCodeStatusResponse struct {
	Ret       int    `json:"ret"`
	Status    string `json:"status"`
	BotToken  string `json:"bot_token"`
	BaseURL   string `json:"baseurl"`
	UserID    string `json:"user_id"`
	AccountID string `json:"account_id"`
	Message   string `json:"message"`
}

type GetUpdatesRequest struct {
	GetUpdatesBuf string          `json:"get_updates_buf"`
	BaseInfo      GetUpdatesBase  `json:"base_info"`
}

type GetUpdatesBase struct {
	ChannelVersion string `json:"channel_version"`
}

type GetUpdatesResponse struct {
	Ret              int              `json:"ret"`
	Msgs             []WeixinMessage  `json:"msgs"`
	GetUpdatesBuf    string           `json:"get_updates_buf"`
	LongPollingTimeout int            `json:"longpolling_timeout_ms"`
	ErrCode          int              `json:"errcode"`
}

type WeixinMessage struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
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
	Msg WeixinMessage `json:"msg"`
}

type SendMessageResponse struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode"`
	Message string `json:"message"`
}

type GetConfigResponse struct {
	Ret          int    `json:"ret"`
	TypingTicket string `json:"typing_ticket"`
}

type SendTypingRequest struct {
	ToUserID     string `json:"to_user_id"`
	TypingTicket string `json:"typing_ticket"`
	ContextToken string `json:"context_token"`
}
