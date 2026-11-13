package board

import (
	"sort"
	"strings"
)

// rank returns the indices of the labels that match q, best match first. An
// empty q matches every label, in order.
func rank(q string, labels []string) []int {
	idx := make([]int, 0, len(labels))
	if q == "" {
		for i := range labels {
			idx = append(idx, i)
		}
		return idx
	}
	score := make([]int, len(labels))
	for i, l := range labels {
		if s, ok := fuzzy(q, l); ok {
			score[i] = s
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return score[idx[a]] < score[idx[b]] })
	return idx
}

// fuzzy matches q as a case-insensitive subsequence of s. A lower score is a tighter match.
func fuzzy(q, s string) (int, bool) {
	// A whole word that equals the query wins, so "ENG" finds ENG before ENGX.
	for _, w := range strings.Fields(s) {
		if strings.EqualFold(w, q) {
			return -1, true
		}
	}
	qr, sr := []rune(strings.ToLower(q)), []rune(strings.ToLower(s))
	score, last, qi := 0, -1, 0
	for i, r := range sr {
		if qi < len(qr) && r == qr[qi] {
			if last >= 0 {
				score += i - last - 1
			} else {
				score += i
			}
			last = i
			qi++
		}
	}
	return score, qi == len(qr)
}
