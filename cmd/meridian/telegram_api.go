package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

var telegramBotHTTPClient = &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

type telegramAPIError struct {
	Code       int
	RetryAfter int
	Reason     string
}

func (e *telegramAPIError) Error() string {
	switch e.Code {
	case 401, 404:
		return "Bot Token 无效，请重新配置"
	case 403:
		return "Bot 没有发送权限，或已被接收者屏蔽"
	case 409:
		return "Bot 已被其他程序或 Webhook 占用，请停止其他接收程序并移除 Webhook"
	case 429:
		return "Telegram 请求过于频繁，稍后自动重试"
	}
	return fmt.Sprintf("Telegram 请求失败（状态 %d）", e.Code)
}

// Never surface Telegram's response text or net/http errors: either can contain
// the credential-bearing request URL. No redirects are followed by either client.
func telegramAPICall(ctx context.Context, client *http.Client, token, method string, payload any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("请先配置 Bot Token")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("无法生成 Telegram 请求")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+url.PathEscape(token)+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("无法生成 Telegram 请求")
	}
	req.Header.Set("Content-Type", "application/json")
	return telegramAPIRequest(ctx, client, req, out)
}

func telegramSendDocument(ctx context.Context, client *http.Client, token string, chatID int64, filename string, data []byte) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("无法生成文件请求")
	}
	part, err := writer.CreateFormFile("document", filename)
	if err != nil {
		return fmt.Errorf("无法生成文件请求")
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("无法生成文件请求")
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("无法生成文件请求")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+url.PathEscape(token)+"/sendDocument", &body)
	if err != nil {
		return fmt.Errorf("无法生成文件请求")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return telegramAPIRequest(ctx, client, req, nil)
}

func telegramAPIRequest(ctx context.Context, client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("无法连接 Telegram，请检查服务器网络")
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return &telegramAPIError{Code: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		code := result.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		reason := ""
		// Retain only a small classification, never Telegram's raw description,
		// which can contain credential-bearing URLs or other sensitive input.
		if code == 400 {
			description := strings.ToLower(result.Description)
			switch {
			case strings.HasPrefix(description, "bad request: message is not modified"):
				reason = "message_not_modified"
			case description == "bad request: message to edit not found", description == "bad request: message can't be edited":
				reason = "message_unavailable"
			case description == "bad request: chat not found":
				reason = "chat_not_found"
			}
		}
		return &telegramAPIError{Code: code, RetryAfter: result.Parameters.RetryAfter, Reason: reason}
	}
	if out != nil {
		if err = json.Unmarshal(result.Result, out); err != nil {
			return fmt.Errorf("Telegram 返回的数据无法解析")
		}
	}
	return nil
}

type telegramUser struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	LanguageCode string `json:"language_code"`
}
type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type telegramMessage struct {
	ID       int64             `json:"message_id"`
	Date     int64             `json:"date"`
	From     telegramUser      `json:"from"`
	Chat     telegramChat      `json:"chat"`
	Text     string            `json:"text"`
	Document *telegramDocument `json:"document,omitempty"`
}

type telegramDocument struct {
	ID   string `json:"file_id"`
	Name string `json:"file_name"`
	Size int64  `json:"file_size"`
}

func (b *telegramBot) downloadImportDocument(ctx context.Context, document telegramDocument) (string, error) {
	if document.Size > 2<<20 {
		return "", fmt.Errorf("导入文件不能超过 2 MB")
	}
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := telegramAPICall(ctx, b.client, b.token, "getFile", map[string]string{"file_id": document.ID}, &file); err != nil {
		return "", err
	}
	if file.Size > 2<<20 || file.Path == "" || strings.HasPrefix(file.Path, "/") || path.Clean(file.Path) != file.Path || strings.ContainsAny(file.Path, "\\?#:") || strings.HasPrefix(file.Path, "..") {
		return "", fmt.Errorf("Telegram 文件路径或大小无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/file/bot"+url.PathEscape(b.token)+"/"+file.Path, nil)
	if err != nil {
		return "", fmt.Errorf("无法生成文件下载请求")
	}
	response, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("无法下载 Telegram 文件")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Telegram 文件下载失败")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return "", fmt.Errorf("文件读取失败或超过 2 MB")
	}
	return string(data), nil
}

type telegramCallback struct {
	ID      string           `json:"id"`
	From    telegramUser     `json:"from"`
	Message *telegramMessage `json:"message"`
	Data    string           `json:"data"`
}
type telegramUpdate struct {
	ID       int64             `json:"update_id"`
	Message  *telegramMessage  `json:"message"`
	Callback *telegramCallback `json:"callback_query"`
}
type telegramButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type telegramKeyboard [][]telegramButton

func telegramButtonFor(text, data string) telegramButton {
	return telegramButton{Text: text, Data: data}
}

type telegramPanelError struct{ err error }

func (e *telegramPanelError) Error() string { return e.err.Error() }
func (e *telegramPanelError) Unwrap() error { return e.err }

// Each administrator has one active panel. Button navigation and wizard text
// input update it in place. Only a missing/deleted panel creates a new message.
func (b *telegramBot) send(ctx context.Context, chatID int64, message string, keyboard telegramKeyboard) error {
	if b.panels == nil {
		b.panels = map[int64]int64{}
	}
	payload := map[string]any{"chat_id": chatID, "text": truncateTelegramText(message, telegramReportMaxMessageBytes), "disable_web_page_preview": true}
	if keyboard == nil {
		keyboard = telegramKeyboard{}
	}
	payload["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	if messageID := b.panels[chatID]; messageID > 0 {
		payload["message_id"] = messageID
		err := telegramAPICall(ctx, b.client, b.token, "editMessageText", payload, nil)
		if err == nil {
			return nil
		}
		var apiErr *telegramAPIError
		if !errors.As(err, &apiErr) {
			return &telegramPanelError{err: err}
		}
		if apiErr.Reason == "message_not_modified" {
			return nil
		}
		if apiErr.Reason != "message_unavailable" {
			return &telegramPanelError{err: err}
		}
		delete(payload, "message_id")
	}
	var sent telegramMessage
	if err := telegramAPICall(ctx, b.client, b.token, "sendMessage", payload, &sent); err != nil {
		return &telegramPanelError{err: err}
	}
	if sent.ID > 0 {
		b.panels[chatID] = sent.ID
	}
	return nil
}
