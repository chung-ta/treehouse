// Package slug derives a worktree directory name and branch name from a
// free-text task description.
//
// A description carrying a YouTrack ticket id is slugged from that id: the
// window starts at TicketPrefix and runs TicketLength runes past it, so
// "add pagination to RV2-72377 BEplutus ROAR" yields "RV2-72377-BEplutus".
// Ticket ids are what tasks are searched, branched, and talked about by, so
// they belong at the front of the name even when the sentence buries them.
//
// Everything else takes the first Length runes. Either way the window is
// chosen BEFORE substitution, so the slug describes the opening words of the
// task, and two descriptions sharing an opening produce the same slug (which
// surfaces as a collision at creation time rather than as two worktrees that
// are hard to tell apart). Whitespace becomes dashes and anything git refuses
// in a ref name is dropped.
package slug

import (
	"fmt"
	"strings"
	"unicode"
)

// MinLength is the shortest accepted description. Descriptions shorter than
// this rarely survive sanitization as a meaningful branch name.
const MinLength = 10

// Length is how many leading characters of a non-ticket description form the
// slug.
const Length = 12

// TicketPrefix marks a YouTrack ticket id. Matched case-sensitively so that
// ordinary prose ("rv2 rewrite", "Revision 2") is not mistaken for a ticket.
const TicketPrefix = "RV2-"

// TicketLength is how many runes past TicketPrefix are kept, enough for the
// issue number plus the start of its title.
const TicketLength = 10

// ErrTooShort reports a description below MinLength.
type ErrTooShort struct {
	Got int
}

func (e *ErrTooShort) Error() string {
	return fmt.Sprintf("description must be at least %d characters, got %d", MinLength, e.Got)
}

// ErrEmptySlug reports a description whose leading characters contain nothing
// usable in a git ref name.
type ErrEmptySlug struct {
	Description string
}

func (e *ErrEmptySlug) Error() string {
	return fmt.Sprintf("description %q has no usable characters in its first %d characters", e.Description, Length)
}

// From converts a task description into a slug used as both the worktree
// directory name and the branch name. It returns an error rather than a
// fallback name: a silently-renamed branch is worse than a rejected one.
func From(description string) (string, error) {
	trimmed := strings.TrimSpace(description)
	if len([]rune(trimmed)) < MinLength {
		return "", &ErrTooShort{Got: len([]rune(trimmed))}
	}

	runes := window([]rune(trimmed))

	var b strings.Builder
	for _, r := range runes {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune('-')
		case isRefUnsafe(r):
			// Dropped rather than substituted: substituting would let two
			// different descriptions collapse onto the same slug more often.
		default:
			b.WriteRune(r)
		}
	}

	out := collapse(b.String())
	if out == "" {
		return "", &ErrEmptySlug{Description: description}
	}
	return out, nil
}

// window picks the run of runes the slug is built from. A description carrying
// a ticket id starts there and keeps TicketLength runes past the prefix, so the
// id leads the name even when the sentence buries it; anything else takes the
// first Length runes. Both windows are clamped to the available input.
func window(runes []rune) []rune {
	if i := strings.Index(string(runes), TicketPrefix); i >= 0 {
		start := len([]rune(string(runes)[:i]))
		end := start + len([]rune(TicketPrefix)) + TicketLength
		if end > len(runes) {
			end = len(runes)
		}
		return runes[start:end]
	}
	if len(runes) > Length {
		return runes[:Length]
	}
	return runes
}

// isRefUnsafe reports whether r is rejected by git-check-ref-format, or is a
// path separator that would silently nest the worktree directory.
func isRefUnsafe(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	return strings.ContainsRune("~^:?*[\\/@.", r)
}

// collapse squeezes runs of dashes and trims the leading and trailing dashes
// that truncating mid-word tends to leave behind ("fix login " -> "fix-login").
func collapse(s string) string {
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}
