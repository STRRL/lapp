package workspace

import (
	"strings"
	"testing"
)

func TestSanitizeDirNameCapsLength(t *testing.T) {
	long := "query text " + strings.Repeat("select pgrst_call from json_to_record ", 100)
	tests := []struct {
		name  string
		input string
	}{
		{name: "long ASCII template", input: long},
		{name: "multibyte characters", input: strings.Repeat("查询 失败 ", 200) + "timeout"},
		{name: "short name unchanged", input: "auth-login-failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeDirName(tt.input)
			if len(got) > maxDirNameLen+9 {
				t.Fatalf("len = %d, want <= %d: %q", len(got), maxDirNameLen+9, got)
			}
			if strings.Trim(got, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
				t.Fatalf("unexpected characters in %q", got)
			}
		})
	}
	if got := sanitizeDirName("auth-login-failed"); got != "auth-login-failed" {
		t.Fatalf("short names must not change, got %q", got)
	}
}

func TestSanitizeDirNameDistinguishesLongNamesWithSamePrefix(t *testing.T) {
	prefix := strings.Repeat("select id from users where ", 10)
	a := sanitizeDirName(prefix + "status = active")
	b := sanitizeDirName(prefix + "status = deleted")
	if a == b {
		t.Fatalf("names differing only after the cut must differ: %q", a)
	}
}
