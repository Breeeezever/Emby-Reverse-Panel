package main

import (
	"context"
	"fmt"
	"strings"
)

func telegramReminderLabel(account SiteAccountInput) string {
	if account.ReminderDays == nil {
		return "提醒未开启"
	}
	if account.ExpiresAt == "" {
		return fmt.Sprintf("提前 %d 天提醒（还需要填写到期时间）", *account.ReminderDays)
	}
	if *account.ReminderDays == 0 {
		return "到期时提醒一次"
	}
	return fmt.Sprintf("提前 %d 天提醒一次", *account.ReminderDays)
}

func telegramAccountMemo(account SiteAccountInput) string {
	value := func(s string) string {
		if s == "" {
			return "未填写"
		}
		return s
	}
	return fmt.Sprintf("👤 %s\n用户名：%s\n登录密码：%s\n安全密码：%s\n\n📅 到期：%s\n🔔 %s\n备注：%s", value(account.AccountName), value(account.Username), value(account.Password), value(account.SecurityPassword), value(account.ExpiresAt), telegramReminderLabel(account), value(account.Note))
}

func (b *telegramBot) draftAccountPrompt(ctx context.Context, id int64, s *telegramSession) error {
	if s.Action != "create" || len(s.Accounts) == 0 {
		return fmt.Errorf("没有待编辑的账号，请重新解析")
	}
	s.AccountIndex = max(0, min(s.AccountIndex, len(s.Accounts)-1))
	choices := []telegramButton{
		telegramButtonFor("🔔 设置到期提醒", "reminder"),
		telegramButtonFor("📅 修改到期时间", "expires"),
		telegramButtonFor("👤 修改账号名称", "name"),
		telegramButtonFor("修改用户名", "username"),
		telegramButtonFor("🔑 修改登录密码", "password"),
		telegramButtonFor("修改安全密码", "security"),
		telegramButtonFor("📝 修改备注", "note"),
	}
	if s.AccountIndex > 0 {
		choices = append(choices, telegramButtonFor("← 上一个账号", "previous"))
	}
	if s.AccountIndex+1 < len(s.Accounts) {
		choices = append(choices, telegramButtonFor("下一个账号 →", "next"))
	}
	choices = append(choices, telegramButtonFor("继续，检查站点 →", "done"))
	message := fmt.Sprintf("🧾 账号草稿 · %d/%d\n\n%s\n\n先核对一下吧，也可以设置提前几天提醒。\n这些内容还没有保存，最后点“保存并启用”才会生效。", s.AccountIndex+1, len(s.Accounts), telegramAccountMemo(s.Accounts[s.AccountIndex]))
	return b.prompt(ctx, id, s, "draft_account", message, choices...)
}

func (b *telegramBot) acceptDraftAccount(ctx context.Context, id int64, s *telegramSession, text string, button bool) error {
	if s.Action != "create" || s.AccountIndex < 0 || s.AccountIndex >= len(s.Accounts) {
		return fmt.Errorf("账号草稿已失效，请重新解析")
	}
	if s.Step == "draft_account" {
		if !button {
			return fmt.Errorf("先点一下要修改的项目，再把新内容发给我吧")
		}
		switch text {
		case "done":
			return b.finishDraft(ctx, id, s, "")
		case "previous":
			s.AccountIndex--
			return b.draftAccountPrompt(ctx, id, s)
		case "next":
			s.AccountIndex++
			return b.draftAccountPrompt(ctx, id, s)
		}
		labels := map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "reminder": "提前提醒天数", "note": "备注"}
		label, ok := labels[text]
		if !ok {
			return fmt.Errorf("请使用当前账号草稿里的按钮")
		}
		s.AccountField = text
		message := "✏️ 把新的" + label + "发给我吧。\n这一步只修改草稿。"
		choices := []telegramButton{telegramButtonFor("↩️ 保持不变", "keep")}
		if text == "reminder" {
			message = "🔔 想提前几天收到提醒？\n\n发送 0–3650 之间的整数，0 表示到期当天。\n同一次到期只提醒一次，不会每天重复推送。"
			choices = append([]telegramButton{telegramButtonFor("提前 3 天", "3"), telegramButtonFor("提前 7 天", "7"), telegramButtonFor("到期当天", "0"), telegramButtonFor("关闭提醒", "clear")}, choices...)
		} else if text == "expires" {
			message = "📅 把到期日期发给我吧。\n例如：2030-11-09，或 2030-11-09 22:08:59。"
			choices = append([]telegramButton{telegramButtonFor("没有到期时间", "clear")}, choices...)
		} else if text == "password" || text == "security" || text == "note" {
			choices = append([]telegramButton{telegramButtonFor("清空这一项", "clear")}, choices...)
		}
		return b.prompt(ctx, id, s, "draft_account_value", message, choices...)
	}
	if button && text == "keep" {
		return b.draftAccountPrompt(ctx, id, s)
	}
	account := s.Accounts[s.AccountIndex]
	clear := button && text == "clear"
	if clear {
		text = ""
	}
	trimmed := strings.TrimSpace(text)
	switch s.AccountField {
	case "name", "username":
		if trimmed == "" || len(trimmed) > 128 {
			return fmt.Errorf("请填写 1–128 字节的账号名称或用户名")
		}
		if s.AccountField == "name" {
			account.AccountName = trimmed
		} else {
			account.Username = trimmed
		}
	case "password":
		account.Password = text
	case "security":
		account.SecurityPassword = text
	case "expires":
		if !clear {
			if _, err := parseSiteAccountExpiry(trimmed); err != nil {
				return fmt.Errorf("这个日期没认出来，试试 2030-11-09 这样的格式吧")
			}
		}
		account.ExpiresAt = trimmed
	case "reminder":
		account.ReminderDays = nil
		if !clear {
			days, err := telegramInteger(trimmed, 0, 3650)
			if err != nil {
				return err
			}
			value := int(days)
			account.ReminderDays = &value
		}
	case "note":
		if len(trimmed) > 2000 {
			return fmt.Errorf("备注稍长了一点，请控制在 2000 字节以内")
		}
		account.Note = trimmed
	default:
		return fmt.Errorf("请重新选择要修改的账号字段")
	}
	s.Accounts[s.AccountIndex] = account
	return b.draftAccountPrompt(ctx, id, s)
}

func telegramIngressLabel(mode string) string {
	if label, ok := map[string]string{"path": "路径模式", "port": "独立端口", "host": "域名模式", "both": "域名与端口"}[mode]; ok {
		return label
	}
	return mode
}

// Keep the active buttons and draft after a validation error, so correcting a
// date or reminder never requires starting the whole wizard again.
func (b *telegramBot) inputError(ctx context.Context, id int64, err error) error {
	message := "💡 这一步还没完成：" + err.Error()
	if s := b.sessions[id]; s != nil && s.PromptMessage != "" {
		return b.send(ctx, id, message+"\n\n"+s.PromptMessage, s.PromptKeyboard)
	}
	return b.send(ctx, id, message+"\n可以回到菜单重新选择。", telegramBackKeyboard())
}
