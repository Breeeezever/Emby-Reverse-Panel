package main

import (
	"strings"
	"testing"
)

func TestFormatSiteAccountExpiryDate(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "fractional seconds", value: "2026-11-09 22:08:59.342128", want: "2026-11-09"},
		{name: "rfc3339", value: "2026-11-09T22:08:59.342128Z", want: "2026-11-09"},
		{name: "date", value: "2026-11-09", want: "2026-11-09"},
		{name: "empty", value: "", want: ""},
		{name: "invalid value remains visible", value: "待修正", want: "待修正"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatSiteAccountExpiryDate(test.value); got != test.want {
				t.Fatalf("formatSiteAccountExpiryDate(%q)=%q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestAccountExpiryDisplayOmitsFractionalSeconds(t *testing.T) {
	value := "2026-11-09 22:08:59.342128"
	memo := telegramAccountMemo(SiteAccountInput{AccountName: "demo", Username: "demo", ExpiresAt: value})
	if !strings.Contains(memo, "📅 到期日：2026-11-09") || strings.Contains(memo, "342128") {
		t.Fatalf("account memo did not use date-only expiry: %q", memo)
	}
	export := renderAccountMemoText(SiteAccountInput{AccountName: "demo", Username: "demo", ExpiresAt: value}, siteExportOptions{IncludeAccounts: true})
	if !strings.Contains(export, "到期日：2026-11-09") || strings.Contains(export, "342128") {
		t.Fatalf("account export did not use date-only expiry: %q", export)
	}
	record, err := parseSiteMessageRecord(export + "\nhttps://origin.example")
	if err != nil || len(record.Accounts) != 1 || record.Accounts[0].ExpiresAt != "2026-11-09" {
		t.Fatalf("exported account expiry was not recognized on paste: %+v, err=%v", record, err)
	}
}
