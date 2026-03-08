package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	repoOwner     = "junhoyeo"
	repoName      = "contrabass"
	checkInterval = 12 * time.Hour
	fetchTimeout  = 3500 * time.Millisecond
	stateDir      = ".contrabass"
	stateFile     = "update-check.json"
	disableEnvVar = "CONTRABASS_NO_UPDATE_CHECK"
)

var releasesURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"

func setGitHubReleasesURL(url string) { releasesURL = url }

var semverRegexp = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type semver struct {
	Major, Minor, Patch int
}

func parseSemver(v string) (semver, bool) {
	m := semverRegexp.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return semver{Major: major, Minor: minor, Patch: patch}, true
}

func IsNewer(current, latest string) bool {
	c, cOk := parseSemver(current)
	l, lOk := parseSemver(latest)
	if !cOk || !lOk {
		return false
	}
	if l.Major != c.Major {
		return l.Major > c.Major
	}
	if l.Minor != c.Minor {
		return l.Minor > c.Minor
	}
	return l.Patch > c.Patch
}

type State struct {
	LastCheckedAt  string `json:"last_checked_at"`
	LastSeenLatest string `json:"last_seen_latest,omitempty"`
}

func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, stateDir, stateFile)
}

func readState() *State {
	p := statePath()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func writeState(s *State) {
	p := statePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o644)
}

func ShouldCheck(now time.Time, state *State) bool {
	if state == nil || state.LastCheckedAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, state.LastCheckedAt)
	if err != nil {
		return true
	}
	return now.Sub(last) >= checkInterval
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func FetchLatestVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

type Result struct {
	Available bool
	Current   string
	Latest    string
}

func Check(ctx context.Context, currentVersion string) Result {
	if os.Getenv(disableEnvVar) == "1" {
		return Result{}
	}
	if currentVersion == "dev" || currentVersion == "" {
		return Result{}
	}

	now := time.Now()
	state := readState()
	if !ShouldCheck(now, state) {
		return Result{}
	}

	latest, err := FetchLatestVersion(ctx)

	writeState(&State{
		LastCheckedAt:  now.Format(time.RFC3339),
		LastSeenLatest: latest,
	})

	if err != nil || latest == "" {
		return Result{}
	}
	if !IsNewer(currentVersion, latest) {
		return Result{}
	}

	return Result{
		Available: true,
		Current:   currentVersion,
		Latest:    latest,
	}
}

func FormatNotification(r Result) string {
	if !r.Available {
		return ""
	}
	return fmt.Sprintf(
		"\n  Update available: %s → %s\n  Run: brew upgrade contrabass\n",
		r.Current, r.Latest,
	)
}
