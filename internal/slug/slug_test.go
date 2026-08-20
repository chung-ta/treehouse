package slug

import (
	"errors"
	"testing"
)

func TestFrom(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        string
	}{
		{"spaces become dashes", "fix login redirect", "fix-login-re"},
		{"exactly ten characters", "fix-logins", "fix-logins"},
		{"trailing dash from truncation is trimmed", "abcdefghijk lmn", "abcdefghijk"},
		{"leading space is trimmed before counting", "   fix login redirect   ", "fix-login-re"},
		{"repeated spaces collapse", "fix   login redirect", "fix-login"},
		{"ref-unsafe characters are dropped", "fix:log*in redirect", "fixlogin-r"},
		{"path separators are dropped", "fix/log/in redirect", "fixlogin-r"},
		{"case is preserved", "Fix Login Redirect", "Fix-Login-Re"},
		{"unicode counts as characters", "修复登录重定向问题处理", "修复登录重定向问题处理"},

		// A ticket id anchors the slug wherever it appears, so the id leads the
		// branch name even when the sentence buries it.
		{"ticket id at the start", "RV2-72377 BEplutus ROAR Missed Call", "RV2-72377-BEpl"},
		{"ticket id mid-sentence", "work on RV2-64171 TEAM1 organization", "RV2-64171-TEAM"},
		{"ticket id with path separator", "RV2-72377/BEplutus-ROAR-Missed", "RV2-72377BEpl"},
		{"ticket window clamps to available input", "RV2-1234 x", "RV2-1234-x"},
		{"lowercase rv2 is not a ticket", "rv2-lowercase rewrite work", "rv2-lowercas"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := From(tc.description)
			if err != nil {
				t.Fatalf("From(%q) returned error: %v", tc.description, err)
			}
			if got != tc.want {
				t.Errorf("From(%q) = %q, want %q", tc.description, got, tc.want)
			}
		})
	}
}

func TestFromRejectsShortDescription(t *testing.T) {
	for _, description := range []string{"", "short", "fix login", "   fix    "} {
		t.Run(description, func(t *testing.T) {
			_, err := From(description)
			var tooShort *ErrTooShort
			if !errors.As(err, &tooShort) {
				t.Fatalf("From(%q) error = %v, want ErrTooShort", description, err)
			}
		})
	}
}

func TestFromRejectsUnusableDescription(t *testing.T) {
	// Enough leading unsafe runes to fill the whole Length window, so nothing
	// usable survives sanitization.
	_, err := From("::::::::::::fix login")
	var empty *ErrEmptySlug
	if !errors.As(err, &empty) {
		t.Fatalf("error = %v, want ErrEmptySlug", err)
	}
}

// A slug is used verbatim as a directory name and a branch name, so it must
// never contain a separator or a character git-check-ref-format rejects.
func TestFromNeverEmitsUnsafeCharacters(t *testing.T) {
	descriptions := []string{
		"fix/login/redirect", "~^:?*[\\ login here", "a.b.c.d.e.f.g", "tab\there\tnow",
	}
	for _, d := range descriptions {
		got, err := From(d)
		if err != nil {
			continue
		}
		for _, r := range got {
			if isRefUnsafe(r) || r == ' ' {
				t.Errorf("From(%q) = %q contains unsafe rune %q", d, got, r)
			}
		}
	}
}
