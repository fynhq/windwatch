package model

import "testing"

func TestTruncateTextPreservesUTF8(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "short", value: "abc", limit: 5, want: "abc"},
		{name: "ascii", value: "abcdef", limit: 3, want: "abc"},
		{name: "multibyte boundary", value: "你好世界", limit: 4, want: "你"},
		{name: "multibyte exact", value: "你好", limit: 6, want: "你好"},
		{name: "non positive limit", value: "abc", limit: 0, want: "abc"},
	}
	for _, test := range tests {
		if got := TruncateText(test.value, test.limit); got != test.want {
			t.Fatalf("%s: TruncateText(%q, %d) = %q, want %q", test.name, test.value, test.limit, got, test.want)
		}
	}
}
