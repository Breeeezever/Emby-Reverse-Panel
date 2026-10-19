package main

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// An account opts in by setting reminder_days. Delivery is recorded separately
// for each expiry/lead time/administrator, so a restart does not send it again.
func (b *telegramBot) deliverAccountReminders(ctx context.Context, now time.Time) error {
	b.app.telegramConfigMu.RLock()
	defer b.app.telegramConfigMu.RUnlock()
	current, err := b.app.db.telegramBotSettings()
	if err != nil {
		return err
	}
	if !current.Enabled || current.Revision != b.settings.Revision {
		return nil
	}
	sites, err := b.app.db.ListSites()
	if err != nil {
		return err
	}
	var deliveryErr error
	for _, site := range sites {
		accounts, err := b.app.db.listSiteAccounts(site.ID)
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if account.ReminderDays == nil || account.ExpiresAt == "" {
				continue
			}
			expires, err := b.app.db.parseSiteAccountExpiry(account.ExpiresAt)
			if err != nil || now.Before(expires.AddDate(0, 0, -*account.ReminderDays)) {
				continue
			}
			action, snoozedUntil, actionErr := b.app.db.accountReminderAction(account.ID, account.ExpiresAt, *account.ReminderDays)
			if actionErr != nil {
				return actionErr
			}
			if action == "handled" || action == "ignored" {
				continue
			}
			if action == "snoozed" {
				if until, parseErr := time.Parse(time.RFC3339, snoozedUntil); parseErr == nil && now.Before(until) {
					continue
				}
			}
			for _, id := range current.AdminIDs {
				var sent int
				if err := b.app.db.db.QueryRow(`SELECT COUNT(*) FROM account_reminder_deliveries WHERE account_id=? AND expires_at=? AND reminder_days=? AND chat_id=?`, account.ID, account.ExpiresAt, *account.ReminderDays, id).Scan(&sent); err != nil {
					return err
				}
				if sent != 0 {
					continue
				}
				state := fmt.Sprintf("将在 %s 到期", formatSiteAccountExpiryDate(account.ExpiresAt))
				if !now.Before(expires) {
					state = "已到期：" + formatSiteAccountExpiryDate(account.ExpiresAt)
				}
				message := fmt.Sprintf("账号到期提醒\n站点：%s\n账号：%s\n%s\n提前提醒：%d 天", site.Name, account.AccountName, state, *account.ReminderDays)
				keyboard := telegramKeyboard{{telegramButtonFor("✅ 已处理", "reminder:"+strconv.FormatInt(account.ID, 10)+":handled"), telegramButtonFor("🙈 忽略本次", "reminder:"+strconv.FormatInt(account.ID, 10)+":ignored")}, {telegramButtonFor("⏰ 明天再提醒", "reminder:"+strconv.FormatInt(account.ID, 10)+":snooze")}}
				if err := telegramAPICall(ctx, b.client, b.token, "sendMessage", map[string]any{"chat_id": id, "text": message, "disable_web_page_preview": true, "reply_markup": map[string]any{"inline_keyboard": keyboard}}, nil); err != nil {
					_ = b.app.db.recordAccountReminderAttempt(account.ID, account.ExpiresAt, *account.ReminderDays, id, "failed", err.Error())
					if deliveryErr == nil {
						deliveryErr = err
					}
					continue
				}
				if err := b.app.db.recordAccountReminderSent(account.ID, account.ExpiresAt, *account.ReminderDays, id); err != nil {
					return err
				}
			}
		}
	}
	return deliveryErr
}

func (b *telegramBot) handleReminderAction(ctx context.Context, chatID, accountID int64, action string) error {
	account, err := b.app.db.getSiteAccountByID(accountID)
	if err != nil || account.ReminderDays == nil || account.ExpiresAt == "" {
		return fmt.Errorf("提醒已失效或账号已更新")
	}
	if action != "handled" && action != "ignored" && action != "snooze" {
		return fmt.Errorf("提醒动作无效")
	}
	status := action
	until := time.Time{}
	message := ""
	switch action {
	case "snooze":
		status = "snoozed"
		until = time.Now().Add(24 * time.Hour)
		message = "⏰ 好的，明天再提醒你。"
	case "handled":
		message = "✅ 已标记为处理，本次到期提醒不会再次发送。"
	case "ignored":
		message = "🙈 已忽略本次到期提醒。"
	}
	if err := b.app.db.saveAccountReminderAction(accountID, account.ExpiresAt, *account.ReminderDays, status, until, fmt.Sprintf("Telegram chat %d", chatID)); err != nil {
		return err
	}
	return b.send(ctx, chatID, message, telegramBackKeyboard())
}
