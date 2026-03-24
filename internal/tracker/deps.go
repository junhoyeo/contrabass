package tracker

import (
	"regexp"
	"strconv"
)

// dependencyLineRe matches lines like "Blocked by: #2, #4", "Depends on: #3", "Requires: #10".
var dependencyLineRe = regexp.MustCompile(`(?i)(?:blocked\s+by|depends[\s-]on|requires):\s*(.+)`)

// issueRefRe extracts "#N" references from a matched dependency line.
var issueRefRe = regexp.MustCompile(`#(\d+)`)

// parseDependencies extracts issue numbers from dependency declarations in an issue body.
// Supported patterns: "Blocked by: #N, #M", "Depends on: #N", "Depends-on: #N", "Requires: #N".
func parseDependencies(body string) []string {
	var deps []string
	seen := make(map[string]bool)
	for _, match := range dependencyLineRe.FindAllStringSubmatch(body, -1) {
		for _, ref := range issueRefRe.FindAllStringSubmatch(match[1], -1) {
			id := ref[1]
			if !seen[id] {
				deps = append(deps, id)
				seen[id] = true
			}
		}
	}
	if deps == nil {
		deps = []string{}
	}
	return deps
}

// ParseBlockedBy extracts issue numbers from "Blocked by: #N, #M" patterns in issue body.
// This is the exported version of parseDependencies for use outside the package.
func ParseBlockedBy(body string) []int {
	strDeps := parseDependencies(body)
	deps := make([]int, 0, len(strDeps))
	for _, s := range strDeps {
		if n, err := strconv.Atoi(s); err == nil {
			deps = append(deps, n)
		}
	}
	return deps
}
