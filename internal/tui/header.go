package tui

import (
	"fmt"
	"image"
	_ "image/png"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/mosaic"
)

var (
	headerLogoOnce sync.Once
	headerLogoArt  string
)

type HeaderData struct {
	RunningAgents  int
	MaxAgents      int
	ThroughputTPS  float64
	RuntimeSeconds int
	TokensIn       int64
	TokensOut      int64
	TokensTotal    int64
	ModelName      string
	ProjectURL     string
	RefreshIn      int
}

type Header struct {
	width int
	data  HeaderData
}

func NewHeader() Header {
	return Header{}
}

func (h Header) Update(data HeaderData) Header {
	h.data = data
	return h
}

func (h Header) SetWidth(w int) Header {
	h.width = w
	return h
}

func (h Header) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	labelStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	line1 := titleStyle.Render("CONTRABASS STATUS")
	if logo := renderHeaderLogo(); logo != "" {
		line1 = lipgloss.JoinHorizontal(lipgloss.Top, logo, "  ", line1)
	}
	line2 := strings.Join([]string{
		labelStyle.Render("Agents: ") + valueStyle.Render(fmt.Sprintf("%d/%d", h.data.RunningAgents, h.data.MaxAgents)),
		labelStyle.Render("Throughput: ") + valueStyle.Render(formatThroughput(h.data.ThroughputTPS, h.data.TokensTotal)),
		labelStyle.Render("Runtime: ") + valueStyle.Render(formatRuntime(h.data.RuntimeSeconds)),
	}, "    ")
	line3 := labelStyle.Render("Tokens: ") + formatTokenLine(labelStyle, valueStyle, h.data)
	scope, fullURL := projectDetails(h.data.ProjectURL)
	line4 := labelStyle.Render("Model: ") + valueStyle.Render(h.data.ModelName) +
		"    " +
		labelStyle.Render("Scope: ") + urlStyle.Render(scope)
	line5 := labelStyle.Render("URL: ") + urlStyle.Render(fullURL)
	line6 := labelStyle.Render(fmt.Sprintf("Refresh in %ds", h.data.RefreshIn))

	content := strings.Join([]string{line1, line2, line3, line4, line5, line6}, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	if h.width > 0 {
		box = box.Width(h.width)
	}

	return box.Render(content)
}

func renderHeaderLogo() string {
	headerLogoOnce.Do(func() {
		f, err := os.Open(".github/assets/contrabass.png")
		if err != nil {
			return
		}
		defer f.Close() //nolint:errcheck

		img, _, err := image.Decode(f)
		if err != nil {
			return
		}

		m := mosaic.New().Width(18).Height(6).Symbol(mosaic.Half)
		headerLogoArt = strings.TrimRight(m.Render(img), "\n")
	})
	return headerLogoArt
}

func formatRuntime(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	m := seconds / 60
	s := seconds % 60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

func formatTokens(n int64) string {
	negative := n < 0
	if negative {
		n = -n
	}
	raw := strconv.FormatInt(n, 10)
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	rem := len(raw) % 3
	if rem > 0 {
		b.WriteString(raw[:rem])
		if len(raw) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(raw); i += 3 {
		b.WriteString(raw[i : i+3])
		if i+3 < len(raw) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

func formatTokenLine(labelStyle lipgloss.Style, valueStyle lipgloss.Style, data HeaderData) string {
	if data.TokensTotal == 0 {
		return lipgloss.NewStyle().Faint(true).Render("collecting...")
	}
	return labelStyle.Render("in: ") + valueStyle.Render(formatTokens(data.TokensIn)) +
		labelStyle.Render(" | out: ") + valueStyle.Render(formatTokens(data.TokensOut)) +
		labelStyle.Render(" | total: ") + valueStyle.Render(formatTokens(data.TokensTotal))
}

func projectDetails(raw string) (scope string, full string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "-", "-"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw, truncateForHeader(raw, 80)
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return u.Host, u.Host
	}
	segments := strings.Split(path, "/")
	scope = path
	if len(segments) >= 4 && segments[1] == "project" {
		scope = segments[0] + "/" + segments[2]
	} else if len(segments) >= 2 && segments[0] == "project" {
		scope = segments[1]
	} else if len(segments) >= 2 {
		scope = segments[0] + "/" + segments[1]
	}
	full = u.Host + "/" + path
	return scope, truncateForHeader(full, 80)
}

func truncateForHeader(s string, max int) string {
	if max <= 3 || len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func formatThroughput(tps float64, tokensTotal int64) string {
	if tokensTotal == 0 {
		return "collecting..."
	}
	raw := fmt.Sprintf("%.1f", tps)
	parts := strings.SplitN(raw, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return raw + " tok/s"
	}
	return formatTokens(whole) + "." + parts[1] + " tok/s"
}
