package dashboard

import (
	"strings"
	"unicode"
)

// fuzzyMatch reports whether query is a subsequence of text (case-insensitive),
// the core of Telescope/fzy matching: characters appear in order, not
// necessarily contiguously. An empty query matches everything.
func fuzzyMatch(query, text string) bool {
	if query == "" {
		return true
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(text))
	i := 0
	for _, r := range t {
		if i < len(q) && r == q[i] {
			i++
			if i == len(q) {
				return true
			}
		}
	}
	return false
}

// fuzzyMatchTerms requires every whitespace-separated term of query to be a
// fuzzy subsequence of text (AND), like fzf/Telescope multi-word patterns.
func fuzzyMatchTerms(query, text string) bool {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		if !fuzzyMatch(term, text) {
			return false
		}
	}
	return true
}

// fuzzyScore returns a Telescope/fzy-style score for how well query matches
// text, or -1 if it does not match. Higher is better. Bonuses favor
// consecutive runs, word boundaries, and early matches.
func fuzzyScore(query, text string) int {
	if query == "" {
		return 0
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(text))
	if len(q) == 0 {
		return 0
	}
	if len(t) == 0 {
		return -1
	}

	// Greedy left-to-right positions for each query rune.
	pos := make([]int, 0, len(q))
	j := 0
	for i, r := range t {
		if j < len(q) && r == q[j] {
			pos = append(pos, i)
			j++
		}
	}
	if j < len(q) {
		return -1
	}

	const (
		scoreMatch       = 16
		scoreGapStart    = -3
		scoreGapExt      = -1
		bonusBoundary    = 8
		bonusCamel       = 7
		bonusConsecutive = 4
		bonusFirstChar   = 10
	)

	score := 0
	prev := -2 // so the first match is not "consecutive"
	for k, p := range pos {
		score += scoreMatch
		if p == 0 {
			score += bonusFirstChar
		} else if isWordBoundary(t, p) {
			score += bonusBoundary
		} else if isCamelBoundary(text, p) {
			// Use original text for camelCase (lower→Upper).
			score += bonusCamel
		}
		if p == prev+1 {
			score += bonusConsecutive
		} else if prev >= 0 {
			gap := p - prev - 1
			score += scoreGapStart + scoreGapExt*(gap-1)
		}
		// Prefer earlier matches slightly.
		if k == 0 {
			score -= p / 4
		}
		prev = p
	}
	return score
}

// fuzzyScoreTerms scores a multi-term query against text. Every term must
// match; the total is the sum of per-term scores. Returns -1 if any term fails.
func fuzzyScoreTerms(query, text string) int {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return 0
	}
	total := 0
	for _, term := range terms {
		s := fuzzyScore(term, text)
		if s < 0 {
			return -1
		}
		total += s
	}
	return total
}

// isWordBoundary is true when text[i] starts a path/word segment.
func isWordBoundary(text []rune, i int) bool {
	if i <= 0 {
		return true
	}
	prev := text[i-1]
	return prev == '/' || prev == '\\' || prev == '-' || prev == '_' ||
		prev == '.' || prev == ' ' || prev == '\t' || prev == '\n' ||
		prev == ':' || prev == '[' || prev == '('
}

// isCamelBoundary detects a lower→Upper transition at i in the original text.
func isCamelBoundary(orig string, i int) bool {
	r := []rune(orig)
	if i <= 0 || i >= len(r) {
		return false
	}
	return unicode.IsLower(r[i-1]) && unicode.IsUpper(r[i])
}
