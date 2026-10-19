package main

import (
	"strings"
	"testing"
	"time"
)

const accountDraftMessage = "名称：草稿站点\n用户名：demo\n登录密码：`original`\n安全密码：`second`\n到期时间：2030-11-09 22:08:59.342128\nhttps://origin.example\nhttps://backup.example"

func assertNoDraftSaved(t *testing.T, b *telegramBot) {
	t.Helper()
	sites, err := b.app.db.ListSites()
	if err != nil || len(sites) != 0 {
		t.Fatalf("draft created a site before save: %v, %d", err, len(sites))
	}
	var accounts int
	if err := b.app.db.db.QueryRow("SELECT COUNT(*) FROM site_accounts").Scan(&accounts); err != nil || accounts != 0 {
		t.Fatalf("draft saved accounts: %v, %d", err, accounts)
	}
}

func TestTelegramRecognizedAccountIsEditableUntilExplicitSave(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	tgText(t, b, "/add")
	tgChoose(t, b, "launch")
	tgText(t, b, accountDraftMessage)
	s := b.sessions[101]
	if s == nil || s.Step != "draft_account" || !strings.Contains(fake.lastText(), "还没有保存") {
		t.Fatalf("missing editable account draft: %s", fake.lastText())
	}
	if s.Draft["dynamic_discovery_enabled"] != true {
		t.Fatal("recognized site did not default to auto discovery")
	}
	assertNoDraftSaved(t, b)
	tgChoose(t, b, "reminder")
	tgText(t, b, "not-a-number")
	if s.Step != "draft_account_value" || !strings.Contains(fake.lastText(), "想提前几天") {
		t.Fatal("invalid input discarded draft or prompt")
	}
	tgChoose(t, b, "7")
	tgChoose(t, b, "password")
	tgText(t, b, " clear ") // Text is a password, not the clear button.
	tgChoose(t, b, "expires")
	tgText(t, b, "2031-12-20 08:09:10")
	tgChoose(t, b, "note")
	tgText(t, b, "renewal memo")
	assertNoDraftSaved(t, b)
	tgChoose(t, b, "done")
	if !strings.Contains(fake.lastText(), "2031-12-20") || strings.Contains(fake.lastText(), "342128") {
		t.Fatal("account draft confirmation exposed fractional expiry seconds")
	}
	if !strings.Contains(fake.lastText(), "提前 7 天") || !strings.Contains(fake.lastText(), "自动发现") {
		t.Fatalf("confirmation lost settings: %s", fake.lastText())
	}
	tgChoose(t, b, "draft_accounts")
	tgChoose(t, b, "done")
	confirm := testTelegramCallback(101, "w:"+s.Nonce+":yes")
	tgHandle(t, b, confirm)
	sites, _ := b.app.db.ListSites()
	if len(sites) != 1 || !sites[0].DynamicDiscoveryEnabled {
		t.Fatalf("save or automatic playback failed: %s", fake.lastText())
	}
	accounts, _ := b.app.db.listSiteAccounts(sites[0].ID)
	if len(accounts) != 1 {
		t.Fatalf("saved %d accounts", len(accounts))
	}
	plain, err := accountPlaintext(accounts[0])
	if err != nil || plain.Password != " clear " || plain.SecurityPassword != "second" || plain.ReminderDays == nil || *plain.ReminderDays != 7 || plain.ExpiresAt != "2031-12-20 08:09:10" || plain.Note != "renewal memo" {
		t.Fatalf("saved account differs from draft: %+v %v", plain, err)
	}
	tgHandle(t, b, confirm)
	sites, _ = b.app.db.ListSites()
	if len(sites) != 1 {
		t.Fatal("reused confirmation duplicated site")
	}
}

func TestTelegramRecognizedDraftCancelExpiryAndValidation(t *testing.T) {
	for _, action := range []string{"cancel", "expired", "invalid-date"} {
		t.Run(action, func(t *testing.T) {
			b, _ := newTestTelegramBot(t)
			tgText(t, b, "/add")
			tgChoose(t, b, "launch")
			tgText(t, b, accountDraftMessage)
			s := b.sessions[101]
			tgChoose(t, b, "done")
			switch action {
			case "cancel":
				tgText(t, b, "/cancel")
			case "expired":
				s.Expires = time.Now().Add(-time.Second)
				tgChoose(t, b, "yes")
			case "invalid-date":
				s.Accounts[0].ExpiresAt = "invalid"
				tgChoose(t, b, "yes")
				if b.sessions[101] != s {
					t.Fatal("validation discarded editable draft")
				}
				tgChoose(t, b, "draft_accounts")
			}
			assertNoDraftSaved(t, b)
		})
	}
}

func TestTelegramRecognizedDraftWithoutDiscoveryKey(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	b.app.dynamicRouteKey = nil
	tgText(t, b, "/add")
	tgChoose(t, b, "launch")
	tgText(t, b, "https://origin.example")
	if b.sessions[101].Draft["dynamic_discovery_enabled"] != false || !strings.Contains(fake.lastText(), "尚未开启自动发现") {
		t.Fatalf("missing capability fallback: %s", fake.lastText())
	}
	assertNoDraftSaved(t, b)
}

func TestSiteMessageRecognizesLabelVariantsWithoutSaving(t *testing.T) {
	for _, message := range []string{
		"## 站点 A\n👤 **用户名**：`demo`\n🔑 登录密码：` pass `\n🔐 安全密码：`second`（仅一次）\n📅 到期日期：2030-11-09\\\nhttps://origin.example",
		"名称：站点 A\n账号：demo\n密码：` pass `\n安全密码：second\n到期时间：2030-11-09\nhttps://origin.example",
		"站点名称：站点 A\nusername: demo\npassword: ` pass `\n二次密码：second\nexpires: 2030-11-09\nhttps://origin.example",
	} {
		record, err := parseSiteMessageRecord(message)
		if err != nil || record.Name != "站点 A" || len(record.Accounts) != 1 {
			t.Fatalf("parse result=%+v err=%v", record, err)
		}
		a := record.Accounts[0]
		if a.Username != "demo" || a.Password != " pass " || a.SecurityPassword != "second" || a.ExpiresAt != "2030-11-09" {
			t.Fatalf("incorrect fields: %+v", a)
		}
	}
	record, err := parseSiteMessageRecord("用户名：demo\n安全密码：second\nhttps://origin.example")
	if err != nil || record.Accounts[0].Password != "" {
		t.Fatal("generic password matched security password")
	}
}

func TestSiteMessagePrefersServerLinesAndIgnoresBoldFieldLabels(t *testing.T) {
	record, err := parseSiteMessageRecord("官网：https://website.example\n**用户名**：demo\n**用户密码**：`pass`\n当前线路：\n[https://primary.example](https://primary.example)\nhttps://backup.example\n客服：https://support.example")
	if err != nil || record.Name != "primary.example" || record.TargetURL != "https://primary.example" || len(record.FailoverTargets) != 1 || record.FailoverTargets[0] != "https://backup.example" || len(record.Accounts) != 1 || record.Accounts[0].Password != "pass" {
		t.Fatalf("wrong announcement fields: %+v %v", record, err)
	}
}

func TestSiteMessageNameAndDomainPathAreIndependent(t *testing.T) {
	for _, tc := range []struct{ message, name, path string }{
		{"名称：原神\nhttps://yuanshen.help", "原神", "yuanshen"},
		{"站点名称：电影站\nhttps://emby.example.co.uk:8443/emby", "电影站", "example"},
		{"## 云影院\n用户名：demo\nhttps://media.example.com", "云影院", "example"},
		{"**雨初**\n用户名：demo\nhttps://video.yuchu.net", "雨初", "yuchu"},
		{"https://yuanshen.help", "yuanshen.help", "yuanshen"},
		{"名称：内网站点\nhttp://127.0.0.1:8096", "内网站点", "site"},
		{"名称：Local\nhttp://localhost:8096", "Local", "localhost"},
	} {
		record, err := parseSiteMessageRecord(tc.message)
		if err != nil || record.Name != tc.name || record.PathPrefix != tc.path {
			t.Errorf("parse %q: name=%q path=%q err=%v", tc.message, record.Name, record.PathPrefix, err)
		}
	}
}

func TestClientExportGroupsAccountsAndLabelsLines(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "light")
	days := 7
	for _, name := range []string{"first", "second"} {
		_, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: name, Password: "test-secret", ExpiresAt: "2030-11-09", ReminderDays: &days})
		if err != nil {
			t.Fatal(err)
		}
	}
	data, _, _, err := b.app.exportSiteData("client", "multi", site.ID, siteExportOptions{true, true})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"1. light", "账号 1", "账号 2", "用户名：first", "登录密码：test-secret", "提前 7 天提醒一次", "🌐 原线路", "🔗 Meridian 反代线路"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "##") || strings.Contains(text, "`") || strings.Count(text, site.TargetURL) != 1 {
		t.Fatalf("unformatted or repeated export: %s", text)
	}
	data, _, _, _ = b.app.exportSiteData("client", "multi", site.ID, siteExportOptions{true, false})
	if strings.Contains(string(data), "test-secret") {
		t.Fatal("excluded password leaked")
	}
}
