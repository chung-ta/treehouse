// Package slug derives a worktree directory name and branch name from a
// free-text task description.
//
// A description carrying a YouTrack ticket id is slugged from that id: the
// window starts at TicketPrefix and runs TicketLength runes past it, so
// "add pagination to RV2-72377 BEplutus ROAR" yields "RV2-72377-BEplutus-ROA".
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

// TicketLength is how many runes past TicketPrefix are kept: the issue number
// plus enough of the title to tell two tasks on one ticket apart. Sized from
// real descriptions — at 10 or 14, "RV2-72377 fix the login bug" and
// "RV2-72377 fix the logout bug" still collapse to the same slug.
const TicketLength = 18

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
	return fmt.Sprintf("description %q has no usable characters in the run used for the slug", e.Description)
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
//
// The prefix must be followed by a digit to count as a ticket. Without that
// check a bare "RV2-" anywhere in the text sanitizes to the 3-rune slug "RV2",
// and because an existing branch is resumed rather than refused, every such
// description would silently share one worktree.
func window(runes []rune) []rune {
	if start := ticketIndex(runes); start >= 0 {
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

// ticketIndex reports the rune index where a ticket id starts, or -1 when the
// description carries none. The prefix must be followed by a digit: a bare
// "RV2-" would otherwise anchor the window on three runes that always survive
// sanitization, so every such description would slug to "RV2" and silently
// resume one another's worktree.
//
// Working in runes throughout avoids reconciling a byte offset from
// strings.Index against a rune-indexed slice.
func ticketIndex(runes []rune) int {
	prefix := []rune(TicketPrefix)
	for i := 0; i+len(prefix) < len(runes); i++ {
		if !matchesAt(runes, i, prefix) {
			continue
		}
		if unicode.IsDigit(runes[i+len(prefix)]) {
			return i
		}
	}
	return -1
}

func matchesAt(runes []rune, i int, prefix []rune) bool {
	for j, r := range prefix {
		if runes[i+j] != r {
			return false
		}
	}
	return true
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
