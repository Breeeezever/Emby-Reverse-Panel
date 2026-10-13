package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type siteImportRequest struct {
	Format          string         `json:"format"`
	Data            string         `json:"data"`
	Preview         bool           `json:"preview"`
	DuplicateAction string         `json:"duplicate_action"`
	Actions         map[int]string `json:"actions"`
	PreviewToken    string         `json:"preview_token"`
}

type siteImportPreviewItem struct {
	Index          int                `json:"index"`
	Name           string             `json:"name"`
	TargetURL      string             `json:"target_url"`
	IngressMode    string             `json:"ingress_mode"`
	PathPrefix     string             `json:"path_prefix"`
	PublicHost     string             `json:"public_host"`
	ListenPort     int                `json:"listen_port"`
	AccountCount   int                `json:"account_count"`
	DuplicateNames []string           `json:"duplicate_names"`
	Action         string             `json:"action"`
	Error          string             `json:"error,omitempty"`
	Status         string             `json:"status,omitempty"`
	Existing       *Site              `json:"-"`
	Payload        map[string]any     `json:"-"`
	Accounts       []SiteAccountInput `json:"-"`
}

type siteImportPreview struct {
	Items []siteImportPreviewItem `json:"items"`
	Token string                  `json:"preview_token"`
	Valid bool                    `json:"valid"`
}

func parseSiteImport(format, data string) ([]siteImportRecord, error) {
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("导入文件不能超过 2 MB")
	}
	var records []siteImportRecord
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		var err error
		records, err = parseSiteCSV(data)
		if err != nil {
			return nil, err
		}
	case "json":
		var envelope struct {
			Version int               `json:"version"`
			Sites   []json.RawMessage `json:"sites"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(data, "\ufeff")), &envelope); err != nil {
			return nil, fmt.Errorf("JSON 导入文件格式无效")
		}
		if envelope.Version != 1 {
			return nil, fmt.Errorf("不支持的 JSON 格式版本，应为 version: 1")
		}
		for _, raw := range envelope.Sites {
			var record siteImportRecord
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &record); err != nil {
				return nil, fmt.Errorf("站点配置格式无效")
			}
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, fmt.Errorf("站点配置格式无效")
			}
			record.Fields = map[string]bool{}
			for key, value := range fields {
				if string(value) != "null" {
					record.Fields[key] = true
				}
			}
			records = append(records, record)
		}
	default:
		return nil, fmt.Errorf("请选择 JSON 或 CSV")
	}
	if len(records) == 0 || len(records) > 500 {
		return nil, fmt.Errorf("导入站点数量应为 1–500 个")
	}
	return records, nil
}

func parseSiteCSV(input string) ([]siteImportRecord, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(input, "\ufeff")))
	reader.FieldsPerRecord = -1 // Older exports used shorter rows for sites without accounts.
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("CSV 文件没有表头")
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	for _, name := range []string{"站点名称", "回源地址"} {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("CSV 缺少 %s 列", name)
		}
	}
	result := []siteImportRecord{}
	byKey := map[string]int{}
	for line := 2; ; line++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行格式无效", line)
		}
		get := func(name string) string {
			if i, ok := columns[name]; ok && i < len(row) {
				return row[i]
			}
			return ""
		}
		name, target := strings.TrimSpace(get("站点名称")), strings.TrimSpace(get("回源地址"))
		if name == "" || target == "" {
			return nil, fmt.Errorf("CSV 第 %d 行缺少站点名称或回源地址", line)
		}
		key := name + "\x00" + target
		index, exists := byKey[key]
		if !exists {
			record := siteImportRecord{Name: name, TargetURL: target, IngressMode: get("入口模式"), PathPrefix: get("路径前缀"), PublicHost: get("访问域名"), Fields: map[string]bool{"name": true, "target_url": true}}
			for column, field := range map[string]string{"入口模式": "ingress_mode", "路径前缀": "path_prefix", "监听端口": "listen_port", "访问域名": "public_host", "备用地址": "failover_targets"} {
				if _, ok := columns[column]; ok {
					record.Fields[field] = true
				}
			}
			if raw := strings.TrimSpace(get("监听端口")); raw != "" {
				port, err := strconv.Atoi(raw)
				if err != nil || port < 0 || port > 65535 {
					return nil, fmt.Errorf("CSV 第 %d 行端口无效", line)
				}
				record.ListenPort = port
			}
			for _, address := range strings.Split(get("备用地址"), "\n") {
				if address = strings.TrimSpace(address); address != "" {
					record.FailoverTargets = append(record.FailoverTargets, address)
				}
			}
			index = len(result)
			byKey[key] = index
			result = append(result, record)
		}
		account := SiteAccountInput{AccountName: get("账号名称"), Username: get("用户名"), Password: get("密码"), SecurityPassword: get("安全密码"), ExpiresAt: get("到期时间"), Note: get("备注")}
		if raw := strings.TrimSpace(get("提前提醒天数")); raw != "" {
			days, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("CSV 第 %d 行提醒天数无效", line)
			}
			account.ReminderDays = &days
		}
		if account.AccountName != "" || account.Username != "" || account.Password != "" || account.SecurityPassword != "" || account.ExpiresAt != "" || account.Note != "" || account.ReminderDays != nil {
			result[index].Accounts = append(result[index].Accounts, account)
		}
	}
	return result, nil
}

func importTargetKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func (a *App) previewSiteImport(ctx context.Context, req siteImportRequest) (siteImportPreview, error) {
	records, err := parseSiteImport(req.Format, req.Data)
	if err != nil {
		return siteImportPreview{}, err
	}
	if req.DuplicateAction == "" {
		req.DuplicateAction = "skip"
	}
	if req.DuplicateAction != "skip" && req.DuplicateAction != "update" && req.DuplicateAction != "new" {
		return siteImportPreview{}, fmt.Errorf("重复项处理方式无效")
	}
	for index, action := range req.Actions {
		if index < 0 || index >= len(records) || (action != "new" && action != "update" && action != "skip") {
			return siteImportPreview{}, fmt.Errorf("导入选项无效")
		}
	}
	sites, err := a.db.ListSites()
	if err != nil {
		return siteImportPreview{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(req.Format + "\x00" + req.Data))
	optionBytes, _ := json.Marshal(struct {
		DuplicateAction string
		Actions         map[int]string
	}{req.DuplicateAction, req.Actions})
	_, _ = hash.Write(optionBytes)
	for _, site := range sites {
		_, _ = fmt.Fprint(hash, siteConfigurationFingerprint(site))
		accounts, err := a.db.listSiteAccounts(site.ID)
		if err != nil {
			return siteImportPreview{}, err
		}
		accountBytes, _ := json.Marshal(accounts)
		_, _ = hash.Write(accountBytes)
	}
	preview := siteImportPreview{Items: []siteImportPreviewItem{}, Token: fmt.Sprintf("%x", hash.Sum(nil)), Valid: true}
	occupied := append([]Site{}, sites...)
	updated := map[int64]bool{}
	for i, record := range records {
		item := siteImportPreviewItem{Index: i, Name: strings.TrimSpace(record.Name), TargetURL: strings.TrimSpace(record.TargetURL), AccountCount: len(record.Accounts), Accounts: record.Accounts, DuplicateNames: []string{}}
		matches := []Site{}
		for _, site := range sites {
			if strings.EqualFold(site.Name, item.Name) || importTargetKey(site.TargetURL) == importTargetKey(item.TargetURL) {
				matches = append(matches, site)
				item.DuplicateNames = append(item.DuplicateNames, site.Name)
			}
		}
		item.Action = "new"
		if len(matches) > 0 {
			item.Action = req.DuplicateAction
		}
		if action := req.Actions[i]; action != "" {
			item.Action = action
		}
		if item.Action == "skip" {
			preview.Items = append(preview.Items, item)
			continue
		}
		if item.Name == "" || item.TargetURL == "" {
			item.Error = "站点名称和回源地址不能为空"
		}
		if item.Action == "update" {
			if len(matches) != 1 {
				item.Error = "没有唯一匹配的已有站点，请选择新增或跳过"
			} else if updated[matches[0].ID] {
				item.Error = "文件内多条记录将更新同一站点，请仅保留一条更新"
			} else {
				existing := matches[0]
				item.Existing = &existing
				updated[existing.ID] = true
			}
		}
		data, _ := json.Marshal(record)
		item.Payload = map[string]any{}
		_ = json.Unmarshal(data, &item.Payload)
		for key := range item.Payload {
			if !record.Fields[key] || key == "accounts" || key == "enabled" {
				delete(item.Payload, key)
			}
		}
		item.Payload["name"], item.Payload["target_url"] = item.Name, item.TargetURL
		if item.Existing != nil && !record.Fields["listen_port"] {
			item.Payload["listen_port"] = item.Existing.ListenPort
		}
		if item.Action == "new" {
			nameUsed := func(name string) bool {
				for _, other := range occupied {
					if strings.EqualFold(other.Name, name) {
						return true
					}
				}
				return false
			}
			base := item.Name
			for n := 2; nameUsed(item.Name); n++ {
				item.Name = fmt.Sprintf("%s (导入 %d)", base, n)
			}
			item.Payload["name"] = item.Name
			mode := record.IngressMode
			if mode == "" {
				mode = ingressModePath
			}
			item.Payload["ingress_mode"] = mode
			prefix := record.PathPrefix
			if mode == ingressModePath {
				if prefix == "" {
					prefix = importSlug(item.Name)
				}
				base := "/" + strings.Trim(prefix, "/")
				prefix = base
				pathUsed := func(value string) bool {
					for _, other := range occupied {
						if strings.TrimRight(other.PathPrefix, "/") == value {
							return true
						}
					}
					return false
				}
				for n := 2; pathUsed(prefix); n++ {
					prefix = fmt.Sprintf("%s-%d", base, n)
				}
				item.Payload["path_prefix"] = prefix
			}
			port := record.ListenPort
			portUsed := port == a.panelListenPort
			for _, other := range occupied {
				if other.ListenPort == port {
					portUsed = true
				}
			}
			if ingressUsesPanel(mode) || port == 0 || portUsed {
				port, err = nextAvailableInternalSitePort(occupied, a.panelListenPort)
				if err != nil {
					item.Error = "无法分配站点端口"
				}
			}
			item.Payload["listen_port"] = port
		}
		accountNames := map[string]bool{}
		for _, account := range item.Accounts {
			normalized, err := normalizeSiteAccountInput(account, nil)
			if err != nil {
				item.Error = err.Error()
				break
			}
			if accountNames[normalized.Username] {
				item.Error = "同一站点中用户名重复"
				break
			}
			accountNames[normalized.Username] = true
		}
		if item.Error == "" {
			action, id, expected := "create", int64(0), ""
			if item.Existing != nil {
				action, id, expected = "update", item.Existing.ID, siteConfigurationFingerprint(*item.Existing)
			}
			result, err := a.executeTelegramSiteCommand(context.WithValue(ctx, siteCommandDryRunKey{}, true), action, id, item.Payload, expected)
			if err != nil {
				item.Error = err.Error()
			} else {
				var candidate Site
				if err := json.Unmarshal(result, &candidate); err != nil {
					return siteImportPreview{}, err
				}
				item.IngressMode, item.PathPrefix, item.PublicHost, item.ListenPort = candidate.IngressMode, candidate.PathPrefix, candidate.PublicHost, candidate.ListenPort
				for _, other := range occupied {
					if item.Existing != nil && other.ID == item.Existing.ID {
						continue
					}
					if other.ListenPort == candidate.ListenPort || (candidate.PathPrefix != "" && other.PathPrefix == candidate.PathPrefix) || (candidate.PublicHost != "" && other.PublicHost == candidate.PublicHost) {
						item.Error = "入口路径、域名或端口与其他站点冲突"
						break
					}
				}
				if item.Existing == nil {
					candidate.ID = -int64(i + 1)
				}
				occupied = append(occupied, candidate)
			}
		}
		if item.Error != "" {
			preview.Valid = false
		}
		preview.Items = append(preview.Items, item)
	}
	return preview, nil
}

type siteImportResult struct {
	Created int                     `json:"created_sites"`
	Updated int                     `json:"updated_sites"`
	Skipped int                     `json:"skipped_sites"`
	Failed  int                     `json:"failed_sites"`
	Items   []siteImportPreviewItem `json:"items"`
}

func (a *App) applySiteImport(ctx context.Context, preview siteImportPreview) siteImportResult {
	result := siteImportResult{Items: preview.Items}
	for i := range result.Items {
		item := &result.Items[i]
		if item.Action == "skip" {
			item.Status = "skipped"
			result.Skipped++
			continue
		}
		action, id, expected := "create", int64(0), ""
		if item.Existing != nil {
			action, id, expected = "update", item.Existing.ID, siteConfigurationFingerprint(*item.Existing)
		}
		if action == "create" {
			item.Payload["accounts"] = item.Accounts
		}
		data, err := a.executeTelegramSiteCommand(context.WithValue(ctx, siteCommandImportKey{}, true), action, id, item.Payload, expected)
		if err == nil && action == "update" {
			var site Site
			err = json.Unmarshal(data, &site)
			if err == nil {
				accounts, readErr := a.db.listSiteAccounts(site.ID)
				err = readErr
				if err == nil {
					for _, input := range item.Accounts {
						normalized, _ := normalizeSiteAccountInput(input, nil)
						accountID := int64(0)
						for _, current := range accounts {
							if current.Username == normalized.Username {
								accountID = current.ID
								break
							}
						}
						if _, err = a.db.saveSiteAccount(site.ID, accountID, input); err != nil {
							break
						}
					}
				}
				if err != nil {
					err = fmt.Errorf("站点配置已保存，但账号保存失败，请检查后重试")
				}
			}
		}
		if err != nil {
			item.Status = "failed"
			item.Error = err.Error()
			result.Failed++
			continue
		}
		if action == "create" {
			item.Status = "created"
			result.Created++
		} else {
			item.Status = "updated"
			result.Updated++
		}
	}
	return result
}

func (a *App) handleSiteDataImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		a.jsonErr(w, 405, "method not allowed")
		return
	}
	var req siteImportRequest
	// The data string is limited to 2 MB after decoding; JSON escaping can
	// expand that string up to sixfold in the enclosing request.
	if err := decodeJSONBodyLimit(w, r, &req, 16<<20); err != nil {
		a.jsonErr(w, 400, "导入请求无效")
		return
	}
	preview, err := a.previewSiteImport(r.Context(), req)
	if err != nil {
		a.jsonErr(w, 400, err.Error())
		return
	}
	if req.Preview {
		a.jsonOK(w, preview)
		return
	}
	if req.PreviewToken == "" || req.PreviewToken != preview.Token {
		a.jsonErr(w, 409, "站点数据或导入选项已变化，请重新预览后确认")
		return
	}
	if !preview.Valid {
		a.jsonErr(w, 400, "请先处理预览中的错误，或跳过对应记录")
		return
	}
	a.jsonOK(w, a.applySiteImport(r.Context(), preview))
}
