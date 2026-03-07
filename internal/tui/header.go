package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/BourgeoisBear/rasterm"
	"github.com/charmbracelet/x/mosaic"
)

// logoBoxCols/logoBoxRows define the logo box content dimensions in terminal cells.
const logoBoxCols = 20
const logoBoxRows = 10

// Exact 2:1 ratio required for square-looking pixels (terminal cells are ~2:1 height:width).
const mosaicLogoCols = 40
const mosaicLogoRows = 20

var (
	headerLogoOnce sync.Once
	headerLogoArt  string // mosaic fallback art (text-safe, passes through cell renderer)
)

// nativeImageEscape holds the pre-rendered Kitty/iTerm escape sequence for the logo.
// This is NOT safe to embed in View() — it must be sent via tea.Raw().
var (
	nativeImageOnce   sync.Once
	nativeImageEscape string // raw escape sequence for native image rendering
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
	TrackerType    string
	TrackerScope   string
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
	// --- Build the stats lines ---
	line1 := titleStyle.Render("CONTRABASS STATUS")
	line2 := strings.Join([]string{
		labelStyle.Render("Agents: ") + valueStyle.Render(fmt.Sprintf("%d/%d", h.data.RunningAgents, h.data.MaxAgents)),
		labelStyle.Render("Throughput: ") + valueStyle.Render(formatThroughput(h.data.ThroughputTPS, h.data.TokensTotal)),
		labelStyle.Render("Runtime: ") + valueStyle.Render(formatRuntime(h.data.RuntimeSeconds)),
	}, "    ")
	line3 := labelStyle.Render("Tokens: ") + formatTokenLine(labelStyle, valueStyle, h.data)
	line4Parts := []string{
		labelStyle.Render("Model: ") + valueStyle.Render(h.data.ModelName),
	}
	if trackerType := strings.TrimSpace(h.data.TrackerType); trackerType != "" {
		line4Parts = append(line4Parts, labelStyle.Render("Tracker: ")+valueStyle.Render(trackerType))
	}

	var line5 string
	if isInternalTrackerType(h.data.TrackerType) {
		line5 = labelStyle.Render("Board: ") + valueStyle.Render(truncateForHeader(displayBoardScope(h.data.TrackerScope), 80))
	} else {
		scope, fullURL := projectDetails(firstNonEmpty(h.data.TrackerScope, h.data.ProjectURL))
		line4Parts = append(line4Parts, labelStyle.Render("Scope: ")+urlStyle.Render(scope))
		line5 = labelStyle.Render("URL: ") + urlStyle.Render(fullURL)
	}

	line4 := strings.Join(line4Parts, "    ")
	line6 := labelStyle.Render(fmt.Sprintf("Refresh in %ds", h.data.RefreshIn))
	statsContent := strings.Join([]string{line1, line2, line3, line4, line5, line6}, "\n")

	outerChrome := 4 // outer border(2) + outer padding(2)
	innerWidth := h.width - outerChrome
	if innerWidth < 40 {
		innerWidth = 40
	}

	logoContent := renderHeaderLogo()
	logoBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240"))
	logoBox := logoBoxStyle.Render(logoContent)
	logoBoxRenderedWidth := lipgloss.Width(logoBox)

	statsBoxChrome := 4 // border(2) + padding(2)
	gap := 1
	statsContentWidth := innerWidth - logoBoxRenderedWidth - gap - statsBoxChrome
	if statsContentWidth < 20 {
		statsContentWidth = 20
	}
	statsBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("133")).
		Padding(0, 1).
		Width(statsContentWidth)
	statsBox := statsBoxStyle.Render(statsContent)

	inner := lipgloss.JoinHorizontal(lipgloss.Bottom, logoBox, " ", statsBox)

	outerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	if h.width > 0 {
		outerBox = outerBox.Width(innerWidth)
	}
	return outerBox.Render(inner)
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

// renderHeaderLogo returns the logo art for embedding in View().
// For native image modes (Kitty/iTerm), it returns blank placeholder lines
// that reserve vertical space for the image rendered via tea.Raw().
// For mosaic mode, it returns the half-block character art directly.
func renderHeaderLogo() string {
	headerLogoOnce.Do(func() {
		mode := detectImageMode()
		switch mode {
		case imageModeKitty, imageModeIterm:
			// Return blank placeholder lines to reserve space for the native image.
			// The actual image is rendered via tea.Raw() bypassing the cell renderer.
			// Each line is a single space to ensure the lines are preserved.
			placeholder := strings.Repeat(" ", logoBoxCols)
			lines := make([]string, logoBoxRows)
			for i := range lines {
				lines[i] = placeholder
			}
			headerLogoArt = strings.Join(lines, "\n")
		default:
			headerLogoArt = renderMosaicLogo()
		}
	})
	return headerLogoArt
}

// initNativeImageEscape pre-renders the native image escape sequence once.
// Must be called before buildNativeImageRaw() is useful.
func initNativeImageEscape() {
	nativeImageOnce.Do(func() {
		mode := detectImageMode()
		if mode == imageModeMosaic {
			return
		}
		f, err := os.Open(".github/assets/contrabass.png")
		if err != nil {
			return
		}
		defer f.Close() //nolint:errcheck
		img, _, err := image.Decode(f)
		if err != nil {
			return
		}
		img = cropToContent(img)
		switch mode {
		case imageModeKitty:
			nativeImageEscape = buildKittyEscape(img)
		case imageModeIterm:
			nativeImageEscape = buildItermEscape(img)
		}
	})
}

// buildNativeImageRaw returns the complete escape sequence string for placing
// the native image at the correct position in alt screen, suitable for tea.Raw().
// Returns empty string if native image is not available.
func buildNativeImageRaw() string {
	initNativeImageEscape()
	if nativeImageEscape == "" {
		return ""
	}
	// In the horizontal two-box layout, the image sits inside the logo box (left),
	// which is inside the outer container box. The cursor position is:
	//   row 3 (1-indexed): outer rounded border (1) + logo box border top (1) + content start
	//   col 4 (1-indexed): outer border (1) + outer padding (1) + logo box border (1) + content start
	//
	// Cursor positioning sequence:
	//   \x1b[s        — save cursor position
	//   \x1b[3;4H     — move to row 3, col 4
	//   <image data>  — Kitty/iTerm escape sequence
	//   \x1b[u        — restore cursor position
	return "\x1b[s\x1b[3;4H" + nativeImageEscape + "\x1b[u"
}

// cleanupNativeImageRaw returns the escape sequence to delete all Kitty images.
// Should be sent via tea.Raw() on quit so the image doesn't persist in the terminal.
func cleanupNativeImageRaw() string {
	if detectImageMode() != imageModeKitty {
		return ""
	}
	// Kitty graphics: a=d (action=delete), d=a (delete all images)
	return "\x1b_Ga=d,d=a\x1b\\"
}

// CleanupNativeImage writes the Kitty delete-all-images escape sequence
// directly to stdout. Call this AFTER p.Run() returns so the cleanup
// happens on the main screen (not the alt-screen which gets discarded).
func CleanupNativeImage() {
	if detectImageMode() != imageModeKitty {
		return
	}
	fmt.Print("\x1b_Ga=d,d=a\x1b\\")
}

// buildKittyEscape renders the image using the Kitty graphics protocol.
// Returns the raw escape sequence string (NOT safe for View()).
func buildKittyEscape(img image.Image) string {
	img = resizeImage(img, 200, 200)

	var buf bytes.Buffer
	opts := rasterm.KittyImgOpts{
		DstCols: logoBoxCols,
		ImageId: 1,
	}
	if err := rasterm.KittyWriteImage(&buf, img, opts); err != nil {
		return ""
	}
	return buf.String()
}

// buildItermEscape renders the image using the iTerm2/WezTerm inline image protocol.
// Returns the raw escape sequence string (NOT safe for View()).
func buildItermEscape(img image.Image) string {
	img = resizeImage(img, 200, 200)

	var buf bytes.Buffer
	opts := rasterm.ItermImgOpts{
		DisplayInline: true,
		Width:         fmt.Sprintf("%d", logoBoxCols),
	}
	if err := rasterm.ItermWriteImageWithOptions(&buf, img, opts); err != nil {
		return ""
	}
	return buf.String()
}

// renderMosaicLogo loads and renders the logo as mosaic half-block characters.
func renderMosaicLogo() string {
	f, err := os.Open(".github/assets/contrabass.png")
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck
	img, _, err := image.Decode(f)
	if err != nil {
		return ""
	}
	img = cropToContent(img)
	pixW := mosaicLogoCols * 2
	pixH := mosaicLogoRows * 2
	alphaMask := buildAlphaMask(img, pixW, pixH)
	img = compositeOnBackground(img)
	return renderMosaicImage(img, alphaMask)
}

func renderMosaicImage(img image.Image, alphaMask [][]bool) string {
	pixW := mosaicLogoCols * 2
	pixH := mosaicLogoRows * 2
	img = resizeToCover(img, pixW, pixW)
	img = resizeImage(img, pixW, pixH)
	img = enhanceContrast(img)
	if alphaMask != nil {
		return renderTransparentMosaic(img, alphaMask)
	}
	m := mosaic.New().Symbol(mosaic.Half)
	return strings.TrimRight(m.Render(img), "\n")
}

func buildAlphaMask(src image.Image, pixW, pixH int) [][]bool {
	resized := resizeToCover(src, pixW, pixW)
	resized = resizeImage(resized, pixW, pixH)
	mask := make([][]bool, pixH)
	for y := 0; y < pixH; y++ {
		mask[y] = make([]bool, pixW)
		for x := 0; x < pixW; x++ {
			_, _, _, a := resized.At(x, y).RGBA()
			mask[y][x] = a > 0x8000
		}
	}
	return mask
}

func renderTransparentMosaic(img image.Image, mask [][]bool) string {
	b := img.Bounds()
	// Mosaic maps 2×2 pixels per terminal cell (2 px wide, 2 px tall per half-block char).
	termCols := b.Dx() / 2
	termRows := b.Dy() / 2
	var buf strings.Builder
	for row := 0; row < termRows; row++ {
		if row > 0 {
			buf.WriteByte('\n')
		}
		for col := 0; col < termCols; col++ {
			px := col * 2
			py := row * 2
			topVis := maskAt(mask, py, px) || maskAt(mask, py, px+1)
			botVis := maskAt(mask, py+1, px) || maskAt(mask, py+1, px+1)
			if !topVis && !botVis {
				buf.WriteByte(' ')
				continue
			}
			tr, tg, tb := avg2(img, b, px, py, px+1, py)
			br, bg, bb := avg2(img, b, px, py+1, px+1, py+1)
			switch {
			case topVis && botVis:
				fmt.Fprintf(&buf, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀\x1b[0m",
					tr, tg, tb, br, bg, bb)
			case topVis:
				fmt.Fprintf(&buf, "\x1b[38;2;%d;%d;%dm▀\x1b[0m", tr, tg, tb)
			default:
				fmt.Fprintf(&buf, "\x1b[38;2;%d;%d;%dm▄\x1b[0m", br, bg, bb)
			}
		}
	}
	return buf.String()
}

func maskAt(mask [][]bool, y, x int) bool {
	if y < 0 || y >= len(mask) {
		return false
	}
	if x < 0 || x >= len(mask[y]) {
		return false
	}
	return mask[y][x]
}

func avg2(img image.Image, b image.Rectangle, x0, y0, x1, y1 int) (uint8, uint8, uint8) {
	r0, g0, b0, _ := img.At(b.Min.X+x0, b.Min.Y+y0).RGBA()
	r1, g1, b1, _ := img.At(b.Min.X+x1, b.Min.Y+y1).RGBA()
	return uint8((r0 + r1) / 2 >> 8), uint8((g0 + g1) / 2 >> 8), uint8((b0 + b1) / 2 >> 8)
}

// enhanceContrast snaps dark pixels darker to make small features like eyes
// render crisply. Only affects pixels below a luminance threshold — bright
// and mid-tone pixels pass through unchanged.
func enhanceContrast(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := src.At(x, y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(bl>>8)
			lum := 0.299*float64(r8) + 0.587*float64(g8) + 0.114*float64(b8)
			// Only darken pixels below luminance threshold (~30% brightness).
			// Scale factor fades from 0.3 (strong darken) at lum=0 to 1.0 (no change) at threshold.
			const threshold = 80.0
			if lum < threshold {
				t := lum / threshold
				scale := 0.3 + 0.7*t*t
				r8 = uint8(float64(r8) * scale)
				g8 = uint8(float64(g8) * scale)
				b8 = uint8(float64(b8) * scale)
			}
			dst.SetRGBA(x-b.Min.X, y-b.Min.Y, color.RGBA{
				R: r8, G: g8, B: b8, A: uint8(a >> 8),
			})
		}
	}
	return dst
}

// resizeToCover scales src to fill targetW×targetH (like CSS object-fit: cover),
// preserving aspect ratio and center-cropping excess.
func resizeToCover(src image.Image, targetW, targetH int) image.Image {
	b := src.Bounds()
	srcW, srcH := float64(b.Dx()), float64(b.Dy())
	if srcW == 0 || srcH == 0 {
		return src
	}
	scale := math.Max(float64(targetW)/srcW, float64(targetH)/srcH)
	w := int(math.Round(srcW * scale))
	h := int(math.Round(srcH * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	resized := resizeImage(src, w, h)
	if w == targetW && h == targetH {
		return resized
	}
	offsetX := (w - targetW) / 2
	offsetY := (h - targetH) / 2
	cropped := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.Draw(cropped, cropped.Bounds(), resized, image.Pt(offsetX, offsetY), draw.Src)
	return cropped
}

func resizeToFit(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	srcW, srcH := float64(b.Dx()), float64(b.Dy())
	if srcW == 0 || srcH == 0 {
		return src
	}
	scale := math.Min(float64(maxW)/srcW, float64(maxH)/srcH)
	w := int(math.Round(srcW * scale))
	h := int(math.Round(srcH * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return resizeImage(src, w, h)
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
			srcXf := (float64(x)+0.5)*float64(srcW)/float64(targetW) - 0.5
			srcYf := (float64(y)+0.5)*float64(srcH)/float64(targetH) - 0.5

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

func isInternalTrackerType(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "internal", "local":
		return true
	default:
		return false
	}
}

func displayBoardScope(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "-"
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
