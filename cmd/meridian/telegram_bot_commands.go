package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

type telegramCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func meridianTelegramCommands() []telegramCommand {
	return []telegramCommand{
		{"start", "打开 Meridian 管理面板"},
		{"menu", "返回主菜单"},
		{"sites", "站点管理"},
		{"add", "添加站点"},
		{"address", "查看站点访问地址"},
		{"traffic", "查看站点流量"},
		{"report", "立即查看日报"},
		{"status", "查看运行概览"},
		{"cancel", "取消当前操作"},
		{"help", "查看帮助"},
		{"id", "查看我的 Telegram ID"},
	}
}

func telegramCommandLanguage(raw string) string {
	language := strings.ToLower(strings.SplitN(strings.ReplaceAll(raw, "_", "-"), "-", 2)[0])
	if len(language) != 2 || language[0] < 'a' || language[0] > 'z' || language[1] < 'a' || language[1] > 'z' {
		return ""
	}
	return language
}

// setMyCommands replaces the complete list atomically for a scope/language;
// old commands are removed without leaving an empty menu if a request fails.
func (b *telegramBot) syncCommandScope(ctx context.Context, scope map[string]any, key, language string) error {
	if b.commandScopes == nil {
		b.commandScopes = map[string]bool{}
	}
	key += "/" + language
	if b.commandScopes[key] {
		return nil
	}
	err := telegramAPICall(ctx, b.client, b.token, "setMyCommands", map[string]any{"scope": scope, "language_code": language, "commands": meridianTelegramCommands()}, nil)
	if err == nil {
		b.commandScopes[key] = true
	}
	return err
}
func (b *telegramBot) syncCommands(ctx context.Context) error {
	for _, scope := range []string{"default", "all_private_chats"} {
		for _, language := range []string{"", "zh", "en"} {
			if err := b.syncCommandScope(ctx, map[string]any{"type": scope}, scope, language); err != nil {
				return err
			}
		}
	}
	if err := telegramAPICall(ctx, b.client, b.token, "setChatMenuButton", map[string]any{"menu_button": map[string]string{"type": "commands"}}, nil); err != nil {
		return err
	}
	for _, id := range b.settings.AdminIDs {
		if err := b.syncAdministratorCommands(ctx, telegramUser{ID: id}); err != nil {
			var apiErr *telegramAPIError
			// An administrator may not have started this bot yet. Configure that chat
			// on its first authorized update instead of blocking every administrator.
			if !errors.As(err, &apiErr) || apiErr.Reason != "chat_not_found" {
				return err
			}
		}
	}
	return nil
}
func (b *telegramBot) syncAdministratorCommands(ctx context.Context, user telegramUser) error {
	key := "chat:" + strconv.FormatInt(user.ID, 10)
	for _, language := range []string{"", "zh", "en", telegramCommandLanguage(user.LanguageCode)} {
		if err := b.syncCommandScope(ctx, map[string]any{"type": "chat", "chat_id": user.ID}, key, language); err != nil {
			return err
		}
	}
	if b.commandScopes[key+"/menu"] {
		return nil
	}
	if err := telegramAPICall(ctx, b.client, b.token, "setChatMenuButton", map[string]any{"chat_id": user.ID, "menu_button": map[string]string{"type": "commands"}}, nil); err != nil {
		return err
	}
	b.commandScopes[key+"/menu"] = true
	return nil
}
