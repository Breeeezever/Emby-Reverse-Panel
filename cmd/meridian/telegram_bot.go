package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
)

type telegramBotStatus struct {
	Revision int64
	State    string
	Message  string
}

func (a *App) setTelegramStatus(revision int64, state, message string) {
	a.telegramStatusMu.Lock()
	defer a.telegramStatusMu.Unlock()
	a.telegramStatus = telegramBotStatus{revision, state, message}
}
func (a *App) telegramPublicSettings(stored telegramReportStoredSettings) TelegramReportSettings {
	s := telegramReportPublicSettings(stored)
	if s.Bot != nil {
		copy := *s.Bot
		s.Bot = &copy
		a.telegramStatusMu.RLock()
		status := a.telegramStatus
		a.telegramStatusMu.RUnlock()
		if !s.Bot.Enabled {
			s.Bot.Status = "disabled"
			s.Bot.StatusMessage = "未启用"
		} else if status.Revision == s.Bot.Revision && status.State != "" {
			s.Bot.Status = status.State
			s.Bot.StatusMessage = status.Message
		} else {
			s.Bot.Status = "connecting"
			s.Bot.StatusMessage = "正在连接 Telegram"
		}
	}
	return s
}

type telegramBot struct {
	app           *App
	settings      TelegramBotSettings
	token         string
	client        *http.Client
	sessions      map[int64]*telegramSession
	panels        map[int64]int64
	commandScopes map[string]bool
}

func (a *App) runTelegramBot(ctx context.Context) {
	var cancel context.CancelFunc
	var done chan struct{}
	var activeRevision int64 = -1
	defer func() {
		if cancel != nil {
			cancel()
			<-done
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if done != nil && cancel != nil {
			select {
			case <-done:
				cancel = nil
				activeRevision = -1
			default:
			}
		}
		a.telegramConfigMu.RLock()
		stored, err := a.db.telegramReportSettings()
		a.telegramConfigMu.RUnlock()
		if err != nil || stored.Bot == nil || !stored.Bot.Enabled || stored.BotTokenCiphertext == "" {
			if cancel != nil {
				cancel()
				<-done
				cancel = nil
				activeRevision = -1
			}
		} else if activeRevision != stored.Bot.Revision {
			if cancel != nil {
				cancel()
				<-done
				cancel = nil
			}
			token, e := decryptTelegramBotToken(stored.BotTokenCiphertext)
			if e != nil {
				a.setTelegramStatus(stored.Bot.Revision, "error", "Bot Token 无法解密，请重新保存")
			} else {
				workerCtx, stop := context.WithCancel(ctx)
				cancel = stop
				done = make(chan struct{})
				activeRevision = stored.Bot.Revision
				bot := &telegramBot{app: a, settings: *stored.Bot, token: token, client: telegramBotHTTPClient, sessions: map[int64]*telegramSession{}}
				go func(finished chan struct{}) { defer close(finished); bot.run(workerCtx) }(done)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func telegramWait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (b *telegramBot) run(ctx context.Context) {
	offset := b.settings.Offset
	backoff := time.Second
	commandsReady := false
	var nextReminderCheck time.Time
	b.app.setTelegramStatus(b.settings.Revision, "connecting", "正在连接 Telegram")
	for ctx.Err() == nil {
		if time.Now().After(nextReminderCheck) {
			if err := b.deliverAccountReminders(ctx, time.Now()); err != nil && ctx.Err() == nil {
				b.app.setTelegramStatus(b.settings.Revision, "error", "到期提醒发送失败，稍后重试")
			}
			nextReminderCheck = time.Now().Add(time.Minute)
		}
		// An initial tail read discards commands left over before enablement, token
		// changes or administrator changes; stored offsets resume ordinary restarts.
		timeout := 20
		if offset < 0 {
			timeout = 0
		}
		var updates []telegramUpdate
		var err error
		if !commandsReady {
			b.app.setTelegramStatus(b.settings.Revision, "connecting", "正在同步 Meridian 命令菜单")
			err = b.syncCommands(ctx)
			commandsReady = err == nil
		}
		if err == nil {
			err = telegramAPICall(ctx, b.client, b.token, "getUpdates", map[string]any{"offset": offset, "timeout": timeout, "limit": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.app.setTelegramStatus(b.settings.Revision, "error", err.Error())
			delay := backoff
			var apiErr *telegramAPIError
			if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
				delay = time.Duration(min(apiErr.RetryAfter, 300)) * time.Second
			}
			if !telegramWait(ctx, delay) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		b.app.setTelegramStatus(b.settings.Revision, "online", "已连接，Meridian 命令已同步；私聊发送 /start 即可操作")
		if offset < 0 {
			offset = 0
			for _, u := range updates {
				offset = max(offset, u.ID+1)
			}
			if !b.storeOffset(offset) {
				return
			}
			continue
		}
		for _, u := range updates {
			if u.ID < offset {
				continue
			}
			offset = u.ID + 1
			// Claim before executing. A crash may interrupt one operation, but cannot
			// replay a destructive command after restart. Confirmation is also single-use.
			if !b.storeOffset(offset) {
				return
			}
			if err = b.handleUpdate(ctx, u); err != nil && ctx.Err() == nil {
				b.app.setTelegramStatus(b.settings.Revision, "error", err.Error())
			}
		}
	}
}
func (b *telegramBot) storeOffset(offset int64) bool {
	result, err := b.app.db.db.Exec(`UPDATE telegram_bot_settings SET update_offset=? WHERE id=1 AND revision=? AND enabled=1`, offset, b.settings.Revision)
	if err != nil {
		b.app.setTelegramStatus(b.settings.Revision, "error", "无法保存 Bot 接收进度，请检查数据库")
		return false
	}
	n, err := result.RowsAffected()
	return err == nil && n == 1
}

func (b *telegramBot) handleUpdate(ctx context.Context, u telegramUpdate) error {
	var from telegramUser
	var chat telegramChat
	var text string
	var date int64
	if u.Message != nil {
		from = u.Message.From
		chat = u.Message.Chat
		text = u.Message.Text
		date = u.Message.Date
	} else if u.Callback != nil && u.Callback.Message != nil {
		from = u.Callback.From
		chat = u.Callback.Message.Chat
		date = u.Callback.Message.Date
	} else {
		return nil
	}
	if from.IsBot || chat.Type != "private" || chat.ID != from.ID || !slices.Contains(b.settings.AdminIDs, from.ID) {
		return nil
	}
	// Serialize against panel credential/allowlist changes, then reauthorize from
	// persisted settings so a revoked administrator cannot finish an old wizard.
	b.app.telegramConfigMu.RLock()
	defer b.app.telegramConfigMu.RUnlock()
	current, err := b.app.db.telegramBotSettings()
	if err != nil || !current.Enabled || current.Revision != b.settings.Revision || !slices.Contains(current.AdminIDs, from.ID) {
		return nil
	}
	if date < current.AuthorizedSince || (u.Message != nil && time.Since(time.Unix(date, 0)) > 10*time.Minute) {
		return nil
	}
	// Adopt the clicked message even after a restart, so navigation edits that
	// panel rather than sending another message. Other users' updates never reach here.
	if b.panels == nil {
		b.panels = map[int64]int64{}
	}
	if u.Callback != nil && u.Callback.Message.ID > 0 {
		b.panels[from.ID] = u.Callback.Message.ID
	}
	if err := b.syncAdministratorCommands(ctx, from); err != nil {
		return err
	}
	if u.Callback != nil {
		if err := telegramAPICall(ctx, b.client, b.token, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, nil); err != nil && ctx.Err() != nil {
			return err
		}
	}
	now := time.Now()
	if session := b.sessions[from.ID]; session != nil && now.After(session.Expires) {
		delete(b.sessions, from.ID)
	}
	if u.Callback != nil {
		if err = b.handleCallback(ctx, from.ID, u.Callback.Data); err != nil {
			var deliveryErr *telegramPanelError
			if errors.As(err, &deliveryErr) {
				return err
			}
			return b.send(ctx, chat.ID, "操作未完成："+err.Error(), telegramBackKeyboard())
		}
		return nil
	}
	if u.Message != nil && u.Message.Document != nil {
		format := strings.TrimPrefix(strings.ToLower(path.Ext(u.Message.Document.Name)), ".")
		if format != "json" && format != "csv" {
			return b.send(ctx, chat.ID, "请发送 JSON 或 CSV 文件；开号消息可通过 /add 粘贴解析。", telegramBackKeyboard())
		}
		data, downloadErr := b.downloadImportDocument(ctx, *u.Message.Document)
		if downloadErr != nil {
			return b.send(ctx, chat.ID, downloadErr.Error(), telegramBackKeyboard())
		}
		s := b.newSession(from.ID, "site-import", nil)
		s.ImportRequest = &siteImportRequest{Format: format, Data: data, Actions: map[int]string{}}
		if err := b.previewImport(ctx, from.ID, s); err != nil {
			return b.send(ctx, chat.ID, "导入预览失败："+err.Error(), telegramBackKeyboard())
		}
		return nil
	}
	if len(text) > 4096 {
		return b.send(ctx, chat.ID, "内容过长，请缩短后重试。", nil)
	}
	if text == "" {
		return b.send(ctx, chat.ID, "请发送文字或点击菜单按钮。", nil)
	}
	if err = b.handleText(ctx, from.ID, text); err != nil {
		var deliveryErr *telegramPanelError
		if errors.As(err, &deliveryErr) {
			return err
		}
		return b.send(ctx, chat.ID, fmt.Sprintf("操作未完成：%s", err), telegramBackKeyboard())
	}
	return nil
}
