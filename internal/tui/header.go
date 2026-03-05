package tui
import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"charm.land/lipgloss/v2"
	"github.com/BourgeoisBear/rasterm"
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

	var logoBlock string
	if logo := renderHeaderLogo(); logo != "" {
		logoBlock = logo + "\n"
	}
	line1 := titleStyle.Render("CONTRABASS STATUS")
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

	content := strings.Join([]string{logoBlock + line1, line2, line3, line4, line5, line6}, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	if h.width > 0 {
		box = box.Width(h.width)
	}

	return box.Render(content)
}

// termImageMode represents the detected terminal image capability.
type termImageMode int

const (
	imageModeMosaic termImageMode = iota // fallback: half-block characters
	imageModeKitty                       // Kitty graphics protocol
	imageModeIterm                       // iTerm2/WezTerm inline images
)

var (
	detectedImageMode     termImageMode
	detectedImageModeOnce sync.Once
)

// detectImageMode checks terminal capabilities once at startup.
func detectImageMode() termImageMode {
	detectedImageModeOnce.Do(func() {
		// Skip native image protocols inside tmux (they don't pass through)
		if os.Getenv("TMUX") != "" || rasterm.IsTmuxScreen() {
			detectedImageMode = imageModeMosaic
			return
		}
		if rasterm.IsKittyCapable() {
			detectedImageMode = imageModeKitty
			return
		}
		if rasterm.IsItermCapable() {
			detectedImageMode = imageModeIterm
			return
		}
		detectedImageMode = imageModeMosaic
	})
	return detectedImageMode
}
func renderHeaderLogo() string {
	if os.Getenv("TMUX") != "" {
		return ""
	}
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
		// Crop to content bounds (remove transparent padding)
		img = cropToContent(img)
		mode := detectImageMode()
		switch mode {
		case imageModeKitty:
			headerLogoArt = renderKittyImage(img)
		case imageModeIterm:
			headerLogoArt = renderItermImage(img)
		default:
			headerLogoArt = renderMosaicImage(img)
		}
	})
	return headerLogoArt
}

// renderKittyImage renders the image using the Kitty graphics protocol.
// The image is sent as base64-encoded PNG with display size specified in terminal cells.
func renderKittyImage(img image.Image) string {
	// Resize for quality — send a reasonably sized image to the terminal.
	// The terminal will handle the final display scaling.
	// Use a square image since the terminal respects aspect ratio.
	img = resizeImage(img, 200, 200)
	img = compositeOnBackground(img)

	var buf bytes.Buffer
	opts := rasterm.KittyImgOpts{
		DstCols: 30, // display width in terminal columns
		DstRows: 15, // display height in terminal rows
	}
	if err := rasterm.KittyWriteImage(&buf, img, opts); err != nil {
		// Fall back to mosaic on error
		return renderMosaicImage(img)
	}
	return buf.String()
}

// renderItermImage renders the image using the iTerm2/WezTerm inline image protocol.
func renderItermImage(img image.Image) string {
	// Resize for quality — send a reasonably sized image.
	img = resizeImage(img, 200, 200)
	img = compositeOnBackground(img)

	var buf bytes.Buffer
	opts := rasterm.ItermImgOpts{
		DisplayInline: true,
		Width:         "30",   // 30 character cells wide
		Height:        "auto", // auto height preserving aspect ratio
	}
	if err := rasterm.ItermWriteImageWithOptions(&buf, img, opts); err != nil {
		// Fall back to mosaic on error
		return renderMosaicImage(img)
	}
	return buf.String()
}

// renderMosaicImage renders the image using mosaic half-block characters (fallback).
func renderMosaicImage(img image.Image) string {
	// Manually resize to 2:1 pixel ratio for visually square output.
	// Mosaic Half: 2px × 2px → 1 terminal char (1 col × 1 row).
	// Terminal chars are ~2x taller than wide, so a 2:1 pixel W:H
	// produces a visually square image.
	// 120×60 px → 60 cols × 30 rows → visually square.
	img = resizeImage(img, 120, 60)
	img = compositeOnBackground(img)
	m := mosaic.New().Symbol(mosaic.Half)
	return strings.TrimRight(m.Render(img), "\n")
}

// resizeImage scales the image to exactly targetW x targetH pixels using bilinear interpolation.
func resizeImage(src image.Image, targetW, targetH int) image.Image {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		for x := 0; x < targetW; x++ {
			// Map destination pixel to source coordinates (float)
			srcXf := (float64(x) + 0.5) * float64(srcW) / float64(targetW) - 0.5
			srcYf := (float64(y) + 0.5) * float64(srcH) / float64(targetH) - 0.5

			// Clamp to valid range
			if srcXf < 0 {
				srcXf = 0
			}
			if srcYf < 0 {
				srcYf = 0
			}
			if srcXf > float64(srcW-1) {
				srcXf = float64(srcW - 1)
			}
			if srcYf > float64(srcH-1) {
				srcYf = float64(srcH - 1)
			}

			// Integer coords and fractional parts
			x0 := int(srcXf)
			y0 := int(srcYf)
			x1 := x0 + 1
			y1 := y0 + 1
			if x1 >= srcW {
				x1 = srcW - 1
			}
			if y1 >= srcH {
				y1 = srcH - 1
			}
			dx := srcXf - float64(x0)
			dy := srcYf - float64(y0)

			// Sample 4 neighboring pixels
			r00, g00, b00, a00 := src.At(b.Min.X+x0, b.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := src.At(b.Min.X+x1, b.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := src.At(b.Min.X+x0, b.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := src.At(b.Min.X+x1, b.Min.Y+y1).RGBA()

			// Bilinear interpolation
			lerp := func(v00, v10, v01, v11 uint32) uint8 {
				top := float64(v00)*(1-dx) + float64(v10)*dx
				bot := float64(v01)*(1-dx) + float64(v11)*dx
				return uint8((top*(1-dy) + bot*dy) / 256)
			}

			dst.SetRGBA(x, y, color.RGBA{
				R: lerp(r00, r10, r01, r11),
				G: lerp(g00, g10, g01, g11),
				B: lerp(b00, b10, b01, b11),
				A: lerp(a00, a10, a01, a11),
			})
		}
	}
	return dst
}
// cropToContent finds the bounding box of non-transparent pixels and crops to it.
func cropToContent(src image.Image) image.Image {
	b := src.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			// Use threshold to ignore anti-aliasing/nearly-transparent pixels
			if a > 0x8000 { // >50% opaque
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	// No content found, return original
	if minX > maxX || minY > maxY {
		return src
	}
	// Crop to content bounds
	cropRect := image.Rect(minX, minY, maxX+1, maxY+1)
	cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	draw.Draw(cropped, cropped.Bounds(), src, cropRect.Min, draw.Src)
	return cropped
}
// compositeOnBackground blends the image onto a dark background,
// handling transparency properly for terminal rendering.
func compositeOnBackground(src image.Image) image.Image {
	b := src.Bounds()
	// Use a dark background that works well with most terminal themes
	bg := color.RGBA{R: 30, G: 30, B: 40, A: 255}
	out := image.NewRGBA(b)
	// Fill with background color first
	draw.Draw(out, out.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	// Composite source image over background (respects alpha)
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Over)
	return out
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
