package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestTelegramLaunchDraftCanEditNameIngressAndPath(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	tgText(t, b, "/import")
	tgHandle(t, b, testTelegramCallback(101, "import:launch"))
	message := "✅ 账户 demo 成功绑定\\\n\\\n· 用户名称 | `demo`\\\n· 用户密码 | `demo-password`\\\n· 安全密码 | `demo-security`（仅发送一次）\\\n· 到期时间 | `2030-11-09 22:08:59.342128`\\\n\\\n· 当前线路：\\\n[https://primary.example:443](https://primary.example:443)\\\n[https://backup.example:443](https://backup.example:443)\\\n请不要泄露服务器地址哦0.0\\\n\\\n· **在【服务器】按钮 - 查看线路和密码**"
	tgText(t, b, message)
	s := b.sessions[101]
	if s == nil || s.Step != "draft_account" || len(s.Accounts) != 1 || s.Accounts[0].Password != "demo-password" {
		t.Fatalf("launch parse failed: %+v / %s", s, fake.lastText())
	}
	tgChoose(t, b, "done")
	tgChoose(t, b, "draft_name")
	tgText(t, b, "My imported server")
	if b.sessions[101].Step != "confirm" || !strings.Contains(fake.lastText(), "My imported server") {
		t.Fatalf("name editing lost draft: %s", fake.lastText())
	}
	tgChoose(t, b, "draft_ingress")
	tgChoose(t, b, "port")
	tgText(t, b, strconv.Itoa(freePort(t)))
	if b.sessions[101].Step != "confirm" || b.sessions[101].Draft["ingress_mode"] != "port" {
		t.Fatal("ingress edit did not return to preview")
	}
	tgChoose(t, b, "draft_ingress")
	tgChoose(t, b, "path")
	tgText(t, b, "/first-path")
	tgChoose(t, b, "draft_address")
	tgText(t, b, "/custom-path")
	if !strings.Contains(fake.lastText(), "https://panel.example.test:8443/custom-path/") {
		t.Fatalf("path preview incorrect: %s", fake.lastText())
	}
	tgChoose(t, b, "yes")
	sites, err := b.app.db.ListSites()
	if err != nil || len(sites) != 1 {
		t.Fatalf("create failed: %v / %s", err, fake.lastText())
	}
	site := sites[0]
	if site.Name != "My imported server" || site.PathPrefix != "/custom-path" || site.IngressMode != "path" || len(site.FailoverTargetList) != 1 || site.FailoverTargetList[0] != "https://backup.example:443" {
		t.Fatalf("imported site=%+v", site)
	}
	accounts, err := b.app.db.listSiteAccounts(site.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatal("account lost after editing draft")
	}
	plain, err := accountPlaintext(accounts[0])
	if err != nil || plain.Password != "demo-password" || plain.SecurityPassword != "demo-security" || plain.ExpiresAt != "2030-11-09 22:08:59.342128" {
		t.Fatalf("memo changed: %+v %v", plain, err)
	}
}

func TestTelegramExportSendsFilesAndIncludesAccountlessSites(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	with := accountWorkflowSite(t, b.app, "memo")
	accountWorkflowSite(t, b.app, "no-memo")
	if _, err := b.app.db.saveSiteAccount(with.ID, 0, SiteAccountInput{Username: "demo", Password: "export-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "client", "json"} {
		tgText(t, b, "/export")
		tgHandle(t, b, testTelegramCallback(101, "export:"+format))
		if format == "client" {
			tgHandle(t, b, testTelegramCallback(101, "export:client:original"))
		}
		tgChoose(t, b, "include")
		tgChoose(t, b, "include")
		if len(fake.documents) == 0 {
			t.Fatalf("%s export did not send a document: %s", format, fake.lastText())
		}
		doc := fake.documents[len(fake.documents)-1]
		ext := format
		if format == "client" {
			ext = "txt"
		}
		if !strings.HasSuffix(doc.Name, "."+ext) || doc.ChatID != "101" || !strings.Contains(string(doc.Data), "no-memo") || !strings.Contains(string(doc.Data), "export-secret") {
			t.Fatalf("bad document=%+v", doc)
		}
	}
	for _, message := range fake.messages {
		if strings.Contains(fmt.Sprint(message["text"]), "export-secret") {
			t.Fatal("export sent credentials in a text message")
		}
	}
	tgHandle(t, b, testTelegramCallback(101, "export:csv"))
	tgChoose(t, b, "exclude")
	doc := fake.documents[len(fake.documents)-1]
	if strings.Contains(string(doc.Data), "export-secret") || strings.Contains(string(doc.Data), "用户名") {
		t.Fatal("account exclusion ignored")
	}
}

func TestTelegramFileImportPreviewsAndUpdatesWithoutErasingPassword(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "existing")
	if _, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", Password: "keep-me"}); err != nil {
		t.Fatal(err)
	}
	fake.importData = "站点名称,回源地址,账号名称,用户名,备注\nexisting,https://existing.example/emby,demo,demo,new note\nno-account,https://empty.example,,,\n"
	message := testTelegramMessage(101, "")
	message.Message.Document = &telegramDocument{ID: "fixture", Name: "sites.csv", Size: int64(len(fake.importData))}
	tgHandle(t, b, message)
	if s := b.sessions[101]; s == nil || s.Step != "import_preview" || len(s.ImportPreview.Items) != 2 {
		t.Fatalf("no import preview: %s", fake.lastText())
	}
	sites, _ := b.app.db.ListSites()
	if len(sites) != 1 {
		t.Fatal("uploaded file was applied before confirmation")
	}
	tgChoose(t, b, "import_action_0_update")
	tgChoose(t, b, "import_yes")
	if !strings.Contains(fake.lastText(), "新增 1、更新 1") {
		t.Fatalf("import failed: %s", fake.lastText())
	}
	accounts, _ := b.app.db.listSiteAccounts(site.ID)
	plain, _ := accountPlaintext(accounts[0])
	if plain.Password != "keep-me" || plain.Note != "new note" {
		t.Fatalf("import damaged account: %+v", plain)
	}
	calls := len(fake.calls)
	message.Message.From.ID = 999
	message.Message.Chat.ID = 999
	if err := b.handleUpdate(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != calls {
		t.Fatal("unauthorized file caused Telegram requests")
	}
}

func TestTelegramMemoAndReminderCanBeManaged(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "memo")
	account, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", Password: "visible-password", SecurityPassword: "visible-security"})
	if err != nil {
		t.Fatal(err)
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:accounts", site.ID)))
	if !strings.Contains(fake.lastText(), "visible-password") || !strings.Contains(fake.lastText(), "visible-security") {
		t.Fatal("memo hides saved passwords")
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:%d:edit", site.ID, account.ID)))
	tgChoose(t, b, "reminder")
	tgText(t, b, "7")
	tgChoose(t, b, "yes")
	stored, _ := b.app.db.getSiteAccount(site.ID, account.ID)
	if stored.ReminderDays == nil || *stored.ReminderDays != 7 {
		t.Fatalf("reminder not saved: %s", fake.lastText())
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:%d:edit", site.ID, account.ID)))
	tgChoose(t, b, "reminder")
	tgChoose(t, b, "clear")
	tgChoose(t, b, "yes")
	stored, _ = b.app.db.getSiteAccount(site.ID, account.ID)
	if stored.ReminderDays != nil {
		t.Fatal("reminder not disabled")
	}
}
