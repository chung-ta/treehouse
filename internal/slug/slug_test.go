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
		{"ticket id at the start", "RV2-72377 BEplutus ROAR Missed Call", "RV2-72377-BEplutus-ROA"},
		{"ticket id mid-sentence", "work on RV2-64171 TEAM1 organization", "RV2-64171-TEAM1-organi"},
		{"ticket id with path separator", "RV2-72377/BEplutus-ROAR-Missed", "RV2-72377BEplutus-ROA"},
		{"ticket window clamps to available input", "RV2-1234 x", "RV2-1234-x"},
		{"lowercase rv2 is not a ticket", "rv2-lowercase rewrite work", "rv2-lowercas"},
		// Pins the byte-offset-to-rune-index conversion in window(). Only
		// multibyte text *before* the ticket id exercises it: a naive
		// runes[i:] would slice mid-character here and still pass every
		// ASCII case above.
		{"multibyte text before ticket id", "修复登录问题 RV2-72377 BEplutus", "RV2-72377-BEplutus"},
		// A bare prefix is not a ticket. Without the digit guard these would
		// all sanitize to "RV2" and silently resume one another's worktree.
		{"bare prefix is not a ticket", "abcdefg RV2-", "abcdefg-RV2"},
		{"bare prefix mid-sentence is not a ticket", "the ticket is RV2-", "the-ticket-i"},
		{"prefix followed by non-digit is not a ticket", "aaaaa RV2-xyz here", "aaaaa-RV2-xy"},
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

// TestFromSeparatesTasksOnOneTicket pins the reason TicketLength is 18. Two
// tasks on one ticket must not land on the same branch: acquire resumes an
// existing branch rather than refusing it, so a collision here is silent — the
// second task checks out the first one's worktree. At TicketLength 10 or 14
// every pair below collapses.
func TestFromSeparatesTasksOnOneTicket(t *testing.T) {
	pairs := [][2]string{
		{"RV2-72377 fix the login bug", "RV2-72377 fix the logout bug"},
		{"RV2-72377 BEplutus ROAR Missed Call", "RV2-72377 BEplutus other thing"},
		{"RV2-75592 BE Alberta agent applications", "RV2-75592 BE Arrakis terminated deals"},
	}

	for _, pair := range pairs {
		first, err := From(pair[0])
		if err != nil {
			t.Fatalf("From(%q) returned error: %v", pair[0], err)
		}
		second, err := From(pair[1])
		if err != nil {
			t.Fatalf("From(%q) returned error: %v", pair[1], err)
		}
		if first == second {
			t.Errorf("From(%q) and From(%q) both = %q, want distinct slugs", pair[0], pair[1], first)
		}
	}
}

// TestFromResumesOneTaskDescribedTwice is the other half of the contract: the
// same task described the same way must produce the same slug, or coming back
// to it starts a second branch instead of resuming the first.
func TestFromResumesOneTaskDescribedTwice(t *testing.T) {
	first, err := From("RV2-72377 BEplutus ROAR Missed Call")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := From("  RV2-72377 BEplutus ROAR Missed Call  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Errorf("same description slugged differently: %q vs %q", first, second)
	}
}
