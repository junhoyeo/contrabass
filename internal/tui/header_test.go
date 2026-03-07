package tui

import (
	"image"
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderViewRenders(t *testing.T) {
	h := NewHeader()
	h = h.Update(HeaderData{RunningAgents: 3, MaxAgents: 10, RuntimeSeconds: 154})
	out := stripANSI(h.View())
	assert.Contains(t, out, "CONTRABASS")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "3/10")
	// Runtime may word-wrap across lines in narrow layouts; check components.
	assert.Contains(t, out, "2m")
	assert.Contains(t, out, "34s")
}

func TestFormatRuntime(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    string
	}{
		{"zero", 0, "0s"},
		{"seconds only", 45, "45s"},
		{"one minute", 60, "1m 0s"},
		{"mixed", 154, "2m 34s"},
		{"negative clamped", -5, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatRuntime(tt.seconds))
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0"},
		{"small", 999, "999"},
		{"thousands", 1234, "1,234"},
		{"millions", 1234567, "1,234,567"},
		{"negative", -1234, "-1,234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatTokens(tt.n))
		})
	}
}

func TestFormatThroughput(t *testing.T) {
	tests := []struct {
		name string
		tps  float64
		want string
	}{
		{"zero", 0.0, "collecting..."},
		{"small", 12.3, "12.3 tok/s"},
		{"large", 1234.5, "1,234.5 tok/s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := int64(1)
			if tt.name == "zero" {
				tokens = 0
			}
			assert.Equal(t, tt.want, formatThroughput(tt.tps, tokens))
		})
	}
}

func TestHeaderZeroData(t *testing.T) {
	h := NewHeader()
	assert.NotPanics(t, func() {
		out := stripANSI(h.View())
		assert.Contains(t, out, "CONTRABASS")
		assert.Contains(t, out, "0/0")
		assert.Contains(t, out, "collecting...")
	})
}

func TestHeaderSetWidth(t *testing.T) {
	h := NewHeader().SetWidth(80)
	h = h.Update(HeaderData{RunningAgents: 1, MaxAgents: 5})
	out := stripANSI(h.View())
	assert.Contains(t, out, "CONTRABASS")
	assert.Contains(t, out, "1/5")
}

func TestHeaderViewShowsInternalTrackerBoard(t *testing.T) {
	h := NewHeader().SetWidth(100)
	h = h.Update(HeaderData{
		ModelName:    "gpt-5",
		TrackerType:  "internal",
		TrackerScope: ".contrabass/board",
	})

	out := stripANSI(h.View())
	assert.Contains(t, out, "Tracker:")
	assert.Contains(t, out, "internal")
	assert.Contains(t, out, "Board:")
	assert.Contains(t, out, ".contrabass/board")
}

func TestHeaderViewShowsRemoteTrackerScope(t *testing.T) {
	h := NewHeader().SetWidth(100)
	h = h.Update(HeaderData{
		ModelName:    "gpt-5",
		TrackerType:  "github",
		TrackerScope: "https://github.com/junhoyeo/contrabass/issues",
	})

	out := stripANSI(h.View())
	assert.Contains(t, out, "Tracker:")
	assert.Contains(t, out, "github")
	assert.Contains(t, out, "Scope:")
	assert.Contains(t, out, "junhoyeo/contrabass")
	assert.Contains(t, out, "URL:")
}

func loadTestImage(t *testing.T) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", ".github", "assets", "contrabass.png"))
	require.NoError(t, err)
	defer f.Close()
	img, _, err := image.Decode(f)
	require.NoError(t, err)
	return img
}

func TestResizeToCoverDimensions(t *testing.T) {
	tests := []struct {
		name             string
		srcW, srcH       int
		targetW, targetH int
	}{
		{"square to landscape", 100, 100, 40, 20},
		{"tall to landscape", 50, 200, 40, 20},
		{"wide to square", 200, 50, 20, 20},
		{"exact match", 40, 20, 40, 20},
		{"contrabass-like", 827, 801, 40, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, tt.srcW, tt.srcH))
			for y := 0; y < tt.srcH; y++ {
				for x := 0; x < tt.srcW; x++ {
					src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
				}
			}
			result := resizeToCover(src, tt.targetW, tt.targetH)
			assert.Equal(t, tt.targetW, result.Bounds().Dx(), "width")
			assert.Equal(t, tt.targetH, result.Bounds().Dy(), "height")
		})
	}
}

func TestResizeToFitDimensions(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 100; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	result := resizeToFit(src, 40, 20)
	b := result.Bounds()
	assert.LessOrEqual(t, b.Dx(), 40, "fit width within bounds")
	assert.LessOrEqual(t, b.Dy(), 20, "fit height within bounds")
	assert.True(t, b.Dx() == 40 || b.Dy() == 20, "fit should touch at least one edge")
}

func TestMosaicImageDimensions(t *testing.T) {
	img := loadTestImage(t)
	img = cropToContent(img)
	rendered := renderMosaicImage(img, nil)
	lines := strings.Split(rendered, "\n")

	assert.Equal(t, mosaicLogoRows, len(lines), "mosaic row count should be %d", mosaicLogoRows)
	for i, line := range lines {
		clean := stripANSI(line)
		cols := len([]rune(clean))
		assert.Equal(t, mosaicLogoCols, cols, "mosaic col count on line %d", i)
	}
}

func TestMosaicImageGolden(t *testing.T) {
	img := loadTestImage(t)
	img = cropToContent(img)
	rendered := renderMosaicImage(img, nil)
	assertGolden(t, "mosaic_logo", stripANSI(rendered))
}
