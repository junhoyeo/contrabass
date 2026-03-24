package tracker

import (
	"regexp"
	"strings"
)

var modelOverridePattern = regexp.MustCompile(`<!--\s*model:\s*(\S+)\s*-->`)

// ParseModelOverride extracts model name from <!-- model: opus --> in issue body.
// Returns empty string if no override found.
func ParseModelOverride(body string) string {
	match := modelOverridePattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	model := strings.TrimSpace(match[1])
	switch model {
	case "opus", "sonnet", "haiku",
		"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5",
		"anthropic/claude-opus-4-6", "anthropic/claude-sonnet-4-6", "anthropic/claude-haiku-4-5":
		return model
	}
	return ""
}
