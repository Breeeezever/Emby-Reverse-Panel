package main

import (
	"context"
	"fmt"
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
			for _, id := range current.AdminIDs {
				var sent int
				if err := b.app.db.db.QueryRow(`SELECT COUNT(*) FROM account_reminder_deliveries WHERE account_id=? AND expires_at=? AND reminder_days=? AND chat_id=?`, account.ID, account.ExpiresAt, *account.ReminderDays, id).Scan(&sent); err != nil {
					return err
				}
				if sent != 0 {
					continue
				}
				state := fmt.Sprintf("将在 %s 到期", account.ExpiresAt)
				if !now.Before(expires) {
					state = "已到期：" + account.ExpiresAt
				}
				message := fmt.Sprintf("账号到期提醒\n站点：%s\n账号：%s\n%s\n提前提醒：%d 天", site.Name, account.AccountName, state, *account.ReminderDays)
				if err := telegramAPICall(ctx, b.client, b.token, "sendMessage", map[string]any{"chat_id": id, "text": message, "disable_web_page_preview": true}, nil); err != nil {
					if deliveryErr == nil {
						deliveryErr = err
					}
					continue
				}
				if _, err := b.app.db.db.Exec(`INSERT OR IGNORE INTO account_reminder_deliveries(account_id, expires_at, reminder_days, chat_id) VALUES(?,?,?,?)`, account.ID, account.ExpiresAt, *account.ReminderDays, id); err != nil {
					return err
				}
			}
		}
	}
	return deliveryErr
}
