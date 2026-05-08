package tracker

import (
	"regexp"
	"strings"
)

var modelOverridePattern = regexp.MustCompile(`<!--\s*model:\s*(\S+)\s*-->`)

// ParseModelOverride extracts a model name from `<!-- model: <name> -->` in the
// issue body and returns the first non-empty value found. Validation of the
// model name (whether the configured agent runtime accepts it) is delegated to
// the runtime — contrabass does not maintain a whitelist, so users can
// reference new models (e.g. gpt-5.5, claude-haiku-5-1) without recompiling.
// An empty payload (`<!-- model: -->`) and a missing override both return "".
func ParseModelOverride(body string) string {
	for _, match := range modelOverridePattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		if model := strings.TrimSpace(match[1]); model != "" {
			return model
		}
	}
	return ""
}
