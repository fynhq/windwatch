package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	LangZH = "zh-CN"
	LangEN = "en"
)

//go:embed locales/*.json
var localeFS embed.FS

type catalog struct {
	messages map[string]string
}

type Catalogs struct {
	byLang map[string]*catalog
}

type Translator struct {
	lang     string
	primary  *catalog
	fallback *catalog
}

func Load() (*Catalogs, error) {
	entries, err := fs.Glob(localeFS, "locales/*.json")
	if err != nil {
		return nil, fmt.Errorf("list locales: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no locale files embedded")
	}

	byLang := make(map[string]*catalog, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(path.Base(entry), ".json")
		lang, ok := canonicalLang(name)
		if !ok {
			return nil, fmt.Errorf("unsupported locale file %q", entry)
		}
		data, err := localeFS.ReadFile(entry)
		if err != nil {
			return nil, fmt.Errorf("read locale %s: %w", lang, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, fmt.Errorf("parse locale %s: %w", lang, err)
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("locale %s is empty", lang)
		}
		byLang[lang] = &catalog{messages: messages}
	}

	fallback, ok := byLang[LangEN]
	if !ok {
		return nil, fmt.Errorf("missing fallback locale %s", LangEN)
	}
	for lang, cat := range byLang {
		if lang == LangEN {
			continue
		}
		if err := compareKeys(lang, cat.messages, LangEN, fallback.messages); err != nil {
			return nil, err
		}
	}
	return &Catalogs{byLang: byLang}, nil
}

func (c *Catalogs) Translator(lang string) *Translator {
	canonical, ok := canonicalLang(lang)
	if !ok {
		canonical = LangEN
	}
	fallback := c.byLang[LangEN]
	primary := c.byLang[canonical]
	if primary == nil {
		primary = fallback
		canonical = LangEN
	}
	return &Translator{lang: canonical, primary: primary, fallback: fallback}
}

func (t *Translator) Lang() string {
	return t.lang
}

func (t *Translator) T(key string) string {
	if value, ok := t.primary.messages[key]; ok {
		return value
	}
	if value, ok := t.fallback.messages[key]; ok {
		return value
	}
	return key
}

func (t *Translator) Tf(key string, args ...any) string {
	return fmt.Sprintf(t.T(key), args...)
}

func Negotiate(cookie, acceptLanguage string) string {
	if lang, ok := canonicalLang(cookie); ok {
		return lang
	}

	bestLang := ""
	bestQuality := -1.0
	bestOrder := 0
	for index, part := range strings.Split(acceptLanguage, ",") {
		fields := strings.Split(part, ";")
		tag := strings.TrimSpace(fields[0])
		quality := 1.0
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if !strings.HasPrefix(field, "q=") {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimPrefix(field, "q="), 64)
			if err != nil {
				continue
			}
			quality = value
		}
		if quality <= 0 {
			continue
		}
		lang, ok := canonicalLang(tag)
		if !ok {
			continue
		}
		if quality > bestQuality || (quality == bestQuality && bestLang != "" && index < bestOrder) {
			bestLang = lang
			bestQuality = quality
			bestOrder = index
		}
	}
	if bestLang != "" {
		return bestLang
	}
	return LangEN
}

func canonicalLang(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	if value == "" {
		return "", false
	}
	base := strings.SplitN(value, "-", 2)[0]
	switch base {
	case "zh":
		return LangZH, true
	case "en":
		return LangEN, true
	default:
		return "", false
	}
}

func compareKeys(lang string, messages map[string]string, fallbackLang string, fallback map[string]string) error {
	var missing, extra []string
	for key := range fallback {
		if _, ok := messages[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range messages {
		if _, ok := fallback[key]; !ok {
			extra = append(extra, key)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Errorf(
		"locale %s keys do not match %s (missing: %v, extra: %v)",
		lang, fallbackLang, missing, extra,
	)
}
