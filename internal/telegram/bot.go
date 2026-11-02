// Package telegram 是精简的 Telegram Bot API 客户端，只包含面板机器人用到的方法
// （原版使用 telegram-bot/api）。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // Telegram 图片为 JPEG
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// Bot 是 Bot API 客户端。
type Bot struct {
	Token   string
	BaseURL string // 默认 https://api.telegram.org，测试时替换
	HTTP    *http.Client
}

// New 创建客户端。
func New(token string) *Bot {
	return &Bot{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Update 是 webhook 收到的更新（只解析用到的字段）。
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message 是一条消息。
type Message struct {
	MessageID      int64       `json:"message_id"`
	From           *User       `json:"from"`
	Chat           Chat        `json:"chat"`
	Text           string      `json:"text"`
	Caption        string      `json:"caption"`
	Photo          []PhotoSize `json:"photo"`
	NewChatMembers []User      `json:"new_chat_members"`
}

// User 是 Telegram 用户。
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// Chat 是会话；私聊的 ID 为正数，群组为负数。
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// PhotoSize 是图片的一个尺寸。
type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// CallbackQuery 是内联键盘按钮的回调。
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// InlineButton 是内联键盘按钮。
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Command 解析消息开头的命令：“/traffic” 或 “/traffic@机器人名”，返回不带斜杠的命令名。
// 带 @ 且不是发给本机器人的命令返回空字符串。
func (m *Message) Command(botName string) string {
	if !strings.HasPrefix(m.Text, "/") {
		return ""
	}
	word, _, _ := strings.Cut(m.Text[1:], " ")
	cmd, target, hasTarget := strings.Cut(word, "@")
	if hasTarget && !strings.EqualFold(target, botName) {
		return ""
	}
	return cmd
}

var errAPI = errors.New("telegram 接口返回错误")

// call 调用 Bot API。错误信息中不包含令牌。
func (b *Bot) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: 构造请求失败", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: 请求失败", method)
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: 返回格式错误", method)
	}
	if !r.OK {
		return fmt.Errorf("%w: %s %s", errAPI, method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// SendMessage 发送文本消息；replyTo 为 0 时不引用，keyboard 为 nil 时不带按钮。
func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string, replyTo int64, keyboard [][]InlineButton) error {
	p := map[string]any{"chat_id": chatID, "text": text}
	if replyTo != 0 {
		p["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	if keyboard != nil {
		p["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	return b.call(ctx, "sendMessage", p, nil)
}

// SendChatAction 显示“正在输入”等状态。
func (b *Bot) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return b.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// AnswerCallbackQuery 结束按钮的加载状态。
func (b *Bot) AnswerCallbackQuery(ctx context.Context, id string) error {
	return b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id}, nil)
}

// SetWebhook 设置 webhook；secret 会由 Telegram 在每次回调时放在 X-Telegram-Bot-Api-Secret-Token 头中。
func (b *Bot) SetWebhook(ctx context.Context, url, secret string) error {
	p := map[string]any{"url": url, "drop_pending_updates": true, "allowed_updates": []string{"message", "callback_query"}}
	if secret != "" {
		p["secret_token"] = secret
	}
	return b.call(ctx, "setWebhook", p, nil)
}

// maxPhotoBytes 是下载图片的大小上限。
const maxPhotoBytes = 10 << 20

// DownloadFile 下载机器人收到的文件。
func (b *Bot) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := b.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.BaseURL+"/file/bot"+b.Token+"/"+f.FilePath, nil)
	if err != nil {
		return nil, errors.New("telegram 下载文件: 构造请求失败")
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("telegram 下载文件: 请求失败")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram 下载文件: 返回 %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes))
}

// DecodeQR 识别图片中的二维码（原版 QRcode::decode，可选 zxing / zbar / 在线接口，这里用纯 Go 的 zxing 移植）。
func DecodeQR(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_TRY_HARDER: true})
	if err != nil {
		return "", err
	}
	return res.GetText(), nil
}
