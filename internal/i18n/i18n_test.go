package i18n

import (
	"testing"
	"time"
)

func TestLoadCatalogs(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	zh := catalogs.Translator(LangZH)
	en := catalogs.Translator(LangEN)
	if got := zh.T("login.title"); got != "登录" {
		t.Fatalf("zh login.title = %q", got)
	}
	if got := en.T("login.title"); got != "Sign in" {
		t.Fatalf("en login.title = %q", got)
	}
	if got := zh.Tf("footer.version", "v0.0.0"); got != "WindWatch v0.0.0" {
		t.Fatalf("zh footer.version = %q", got)
	}
	if got := zh.T("missing.key"); got != "missing.key" {
		t.Fatalf("missing key = %q", got)
	}
}

func TestNegotiate(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		accept string
		want   string
	}{
		{name: "cookie wins", cookie: "zh-CN", accept: "en-US", want: LangZH},
		{name: "chinese header", accept: "zh-CN,zh;q=0.9,en;q=0.8", want: LangZH},
		{name: "english header", accept: "en-US,en;q=0.9", want: LangEN},
		{name: "quality", accept: "en;q=0.4,zh;q=0.9", want: LangZH},
		{name: "fallback", accept: "fr-FR", want: LangEN},
		{name: "underscore cookie", cookie: "zh_CN", want: LangZH},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Negotiate(test.cookie, test.accept); got != test.want {
				t.Fatalf("Negotiate(%q, %q) = %q, want %q", test.cookie, test.accept, got, test.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	zh := catalogs.Translator(LangZH)
	en := catalogs.Translator(LangEN)
	if got := FormatDuration(zh, 25*time.Hour+2*time.Minute); got != "1天 1小时 2分" {
		t.Fatalf("zh duration = %q", got)
	}
	if got := FormatDuration(en, 90*time.Second); got != "1m 30s" {
		t.Fatalf("en duration = %q", got)
	}
	if got := FormatDuration(en, 0); got != "—" {
		t.Fatalf("zero duration = %q", got)
	}
}
