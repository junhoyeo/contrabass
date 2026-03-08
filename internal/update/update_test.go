package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   semver
		wantOk bool
	}{
		{"bare version", "1.2.3", semver{1, 2, 3}, true},
		{"v prefix", "v1.2.3", semver{1, 2, 3}, true},
		{"zero patch", "0.1.0", semver{0, 1, 0}, true},
		{"large numbers", "10.20.300", semver{10, 20, 300}, true},
		{"whitespace", "  v1.0.0  ", semver{1, 0, 0}, true},
		{"empty", "", semver{}, false},
		{"no minor", "1.2", semver{}, false},
		{"alpha suffix", "1.2.3-alpha", semver{}, false},
		{"garbage", "abc", semver{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseSemver(tt.input)
			assert.Equal(t, tt.wantOk, ok)
			if ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"patch bump", "0.1.0", "0.1.1", true},
		{"minor bump", "0.1.0", "0.2.0", true},
		{"major bump", "0.1.0", "1.0.0", true},
		{"same version", "0.1.0", "0.1.0", false},
		{"older version", "0.2.0", "0.1.0", false},
		{"with v prefix", "v0.1.0", "v0.1.1", true},
		{"mixed prefix", "0.1.0", "v0.1.1", true},
		{"invalid current", "abc", "0.1.1", false},
		{"invalid latest", "0.1.0", "abc", false},
		{"both invalid", "abc", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsNewer(tt.current, tt.latest))
		})
	}
}

func TestShouldCheck(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		state *State
		want  bool
	}{
		{"nil state", nil, true},
		{"empty timestamp", &State{LastCheckedAt: ""}, true},
		{"invalid timestamp", &State{LastCheckedAt: "not-a-date"}, true},
		{
			"checked 1 hour ago",
			&State{LastCheckedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
			false,
		},
		{
			"checked 13 hours ago",
			&State{LastCheckedAt: now.Add(-13 * time.Hour).Format(time.RFC3339)},
			true,
		},
		{
			"checked exactly 12 hours ago",
			&State{LastCheckedAt: now.Add(-12 * time.Hour).Format(time.RFC3339)},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShouldCheck(now, tt.state))
		})
	}
}

func TestFetchLatestVersion(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.0"})
		}))
		defer server.Close()

		orig := releasesURL
		defer func() { setGitHubReleasesURL(orig) }()
		setGitHubReleasesURL(server.URL)

		v, err := FetchLatestVersion(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "0.2.0", v)
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		orig := releasesURL
		defer func() { setGitHubReleasesURL(orig) }()
		setGitHubReleasesURL(server.URL)

		_, err := FetchLatestVersion(context.Background())
		require.Error(t, err)
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := FetchLatestVersion(ctx)
		require.Error(t, err)
	})
}

func TestCheck(t *testing.T) {
	t.Run("skips dev version", func(t *testing.T) {
		r := Check(context.Background(), "dev")
		assert.False(t, r.Available)
	})

	t.Run("skips empty version", func(t *testing.T) {
		r := Check(context.Background(), "")
		assert.False(t, r.Available)
	})

	t.Run("skips when disabled via env", func(t *testing.T) {
		t.Setenv("CONTRABASS_NO_UPDATE_CHECK", "1")
		r := Check(context.Background(), "0.1.0")
		assert.False(t, r.Available)
	})
}

func TestFormatNotification(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		msg := FormatNotification(Result{Available: true, Current: "0.1.0", Latest: "0.2.0"})
		assert.Contains(t, msg, "0.1.0")
		assert.Contains(t, msg, "0.2.0")
		assert.Contains(t, msg, "brew upgrade contrabass")
	})

	t.Run("not available", func(t *testing.T) {
		msg := FormatNotification(Result{})
		assert.Empty(t, msg)
	})
}

func TestStateReadWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, stateFile)

	s := &State{
		LastCheckedAt:  "2026-03-08T12:00:00Z",
		LastSeenLatest: "0.2.0",
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, data, 0o644))

	read, err := os.ReadFile(p)
	require.NoError(t, err)

	var loaded State
	require.NoError(t, json.Unmarshal(read, &loaded))
	assert.Equal(t, s.LastCheckedAt, loaded.LastCheckedAt)
	assert.Equal(t, s.LastSeenLatest, loaded.LastSeenLatest)
}
