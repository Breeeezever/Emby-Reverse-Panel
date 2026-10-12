package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type siteExportOptions struct {
	IncludeAccounts  bool `json:"include_accounts"`
	IncludePasswords bool `json:"include_passwords"`
}

func (a *App) exportSiteData(format, mode string, siteID int64, opts siteExportOptions) ([]byte, string, string, error) {
	if format != "json" && format != "csv" && format != "client" {
		return nil, "", "", fmt.Errorf("请选择 JSON、CSV 或客户端文本")
	}
	if mode == "" {
		mode = "multi"
	}
	if mode != "original" && mode != "proxy" && mode != "multi" {
		return nil, "", "", fmt.Errorf("线路模式无效")
	}
	records, err := a.siteDataRecords(opts)
	if err != nil {
		return nil, "", "", fmt.Errorf("读取导出数据失败")
	}
	if siteID > 0 {
		filtered := make([]siteImportRecord, 0, 1)
		for _, record := range records {
			if record.ID == siteID {
				filtered = append(filtered, record)
			}
		}
		if len(filtered) == 0 {
			return nil, "", "", fmt.Errorf("站点不存在")
		}
		records = filtered
	}
	if format == "json" {
		data, err := json.MarshalIndent(siteDataEnvelope{Version: 1, Sites: records}, "", "  ")
		return data, "meridian-sites.json", "application/json; charset=utf-8", err
	}
	var buffer bytes.Buffer
	if format == "csv" {
		// A BOM lets spreadsheet applications recognize Chinese column names.
		buffer.WriteString("\xef\xbb\xbf")
		writer := csv.NewWriter(&buffer)
		header := []string{"站点名称", "回源地址", "入口模式", "路径前缀", "监听端口", "访问域名", "备用地址"}
		if opts.IncludeAccounts {
			header = append(header, "账号名称", "用户名", "到期时间", "备注", "提前提醒天数")
			if opts.IncludePasswords {
				header = append(header, "密码", "安全密码")
			}
		}
		_ = writer.Write(header)
		for _, record := range records {
			accounts := record.Accounts
			if len(accounts) == 0 {
				accounts = []SiteAccountInput{{}}
			}
			for _, account := range accounts {
				row := []string{record.Name, record.TargetURL, record.IngressMode, record.PathPrefix, strconv.Itoa(record.ListenPort), record.PublicHost, strings.Join(record.FailoverTargets, "\n")}
				if opts.IncludeAccounts {
					days := ""
					if account.ReminderDays != nil {
						days = strconv.Itoa(*account.ReminderDays)
					}
					row = append(row, account.AccountName, account.Username, account.ExpiresAt, account.Note, days)
					if opts.IncludePasswords {
						row = append(row, account.Password, account.SecurityPassword)
					}
				}
				if err := writer.Write(row); err != nil {
					return nil, "", "", err
				}
			}
		}
		writer.Flush()
		return buffer.Bytes(), "meridian-sites.csv", "text/csv; charset=utf-8", writer.Error()
	}
	for _, record := range records {
		fmt.Fprintf(&buffer, "## %s\n", record.Name)
		accounts := record.Accounts
		if len(accounts) == 0 {
			accounts = []SiteAccountInput{{}}
		}
		site := Site{ID: record.ID, Name: record.Name, TargetURL: record.TargetURL, FailoverTargetList: record.FailoverTargets, IngressMode: record.IngressMode, PathPrefix: record.PathPrefix, PublicHost: record.PublicHost, ListenPort: record.ListenPort}
		for _, account := range accounts {
			buffer.WriteString(a.renderAccountClientText(site, account, mode, opts))
			buffer.WriteByte('\n')
		}
	}
	return buffer.Bytes(), "meridian-sites.txt", "text/plain; charset=utf-8", nil
}

func (a *App) renderAccountClientText(site Site, account SiteAccountInput, mode string, opts siteExportOptions) string {
	var out strings.Builder
	if opts.IncludeAccounts && (account.AccountName != "" || account.Username != "") {
		fmt.Fprintf(&out, "· 用户名称 | `%s`\n", account.Username)
		if opts.IncludePasswords {
			fmt.Fprintf(&out, "· 用户密码 | `%s`\n", account.Password)
			if account.SecurityPassword != "" {
				fmt.Fprintf(&out, "· 安全密码 | `%s`\n", account.SecurityPassword)
			}
		}
		if account.ExpiresAt != "" {
			fmt.Fprintf(&out, "· 到期时间 | `%s`\n", account.ExpiresAt)
		}
		if account.Note != "" {
			fmt.Fprintf(&out, "· 备注 | %s\n", account.Note)
		}
	}
	out.WriteString("\n· 当前线路：\n")
	seen := map[string]bool{}
	lines := []string{}
	if mode == "original" || mode == "multi" {
		lines = append(lines, site.TargetURL)
		lines = append(lines, site.FailoverTargetList...)
	}
	if mode == "proxy" || mode == "multi" {
		lines = append(lines, a.siteProxyLines(site)...)
	}
	for _, line := range lines {
		if line != "" && !seen[line] {
			seen[line] = true
			fmt.Fprintln(&out, line)
		}
	}
	return out.String()
}

func (a *App) handleSiteDataExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		a.jsonErr(w, 405, "method not allowed")
		return
	}
	opts := siteExportOptions{IncludeAccounts: true}
	for key, target := range map[string]*bool{"include_accounts": &opts.IncludeAccounts, "include_passwords": &opts.IncludePasswords} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				a.jsonErr(w, 400, "导出选项无效")
				return
			}
			*target = value
		}
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	data, name, contentType, err := a.exportSiteData(format, r.URL.Query().Get("line_mode"), 0, opts)
	if err != nil {
		a.jsonErr(w, 400, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(data)
}
