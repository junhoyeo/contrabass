package tracker

import (
	"regexp"
	"strings"
)

var modelOverridePattern = regexp.MustCompile(`<!--\s*model:\s*(\S+)\s*-->`)

var validModelOverrides = map[string]struct{}{
	"opus":                        {},
	"sonnet":                      {},
	"haiku":                       {},
	"claude-opus-4-5":             {},
	"claude-opus-4-6":             {},
	"claude-sonnet-4-6":           {},
	"claude-haiku-4-5":            {},
	"anthropic/claude-opus-4-5":   {},
	"anthropic/claude-opus-4-6":   {},
	"anthropic/claude-sonnet-4-6": {},
	"anthropic/claude-haiku-4-5":  {},
}

// ParseModelOverride extracts model name from <!-- model: opus --> in issue body.
// Returns the first valid override and ignores invalid values.
func ParseModelOverride(body string) string {
	for _, match := range modelOverridePattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		model := strings.TrimSpace(match[1])
		if _, ok := validModelOverrides[model]; ok {
			return model
		}
	}
	return ""
}
