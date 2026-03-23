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
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}

type CDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	EncryptType       int    `json:"encrypt_type,omitempty"`
}

type ImageItem struct {
	Media   *CDNMedia `json:"media,omitempty"`
	AESKey  string    `json:"aeskey,omitempty"`
	MidSize int       `json:"mid_size,omitempty"`
}

type VoiceItem struct {
	Text string `json:"text,omitempty"` // 微信服务端语音转写结果
}

type FileItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	FileName string    `json:"file_name,omitempty"`
	MD5      string    `json:"md5,omitempty"`
	Len      string    `json:"len,omitempty"`
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

type GetUploadURLRequest struct {
	FileKey     string   `json:"filekey"`
	MediaType   int      `json:"media_type"`
	ToUserID    string   `json:"to_user_id"`
	RawSize     int      `json:"rawsize"`
	RawFileMD5  string   `json:"rawfilemd5"`
	FileSize    int      `json:"filesize"`
	NoNeedThumb bool     `json:"no_need_thumb"`
	AESKey      string   `json:"aeskey"`
	BaseInfo    BaseInfo `json:"base_info"`
}

type GetUploadURLResponse struct {
	Ret              int    `json:"ret"`
	ErrCode          int    `json:"errcode"`
	Message          string `json:"message"`
	UploadParam      string `json:"upload_param"`
	ThumbUploadParam string `json:"thumb_upload_param"`
}
