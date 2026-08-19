// Package slug derives a worktree directory name and branch name from a
// free-text task description.
//
// The rule is deliberately literal: take the first MinLength characters of the
// description, turn whitespace into dashes, and drop anything git refuses in a
// ref name. Truncation happens BEFORE substitution so the slug always describes
// the opening words of the task, and two descriptions that share an opening
// produce the same slug (which surfaces as a collision at creation time rather
// than as two worktrees that are hard to tell apart).
package slug

import (
	"fmt"
	"strings"
	"unicode"
)

// MinLength is the shortest accepted description. Descriptions shorter than
// this rarely survive sanitization as a meaningful branch name.
const MinLength = 10

// Length is how many leading characters of the description form the slug.
const Length = 10

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

	runes := []rune(trimmed)
	if len(runes) > Length {
		runes = runes[:Length]
	}

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
