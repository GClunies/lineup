// Package markdown renders Linear's Markdown (Issue descriptions) for the terminal.
package markdown

import (
	"crypto/sha256"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/gclunies/lineup/internal/termsafe"
)

// Renderer caches rendered lines per text and width: rendering runs chroma
// for code blocks. Returned slices are shared, so callers must not change them.
type Renderer struct {
	dark  bool
	mu    sync.Mutex
	cache map[string][]string
}

func New(dark bool) *Renderer {
	return &Renderer{dark: dark, cache: map[string][]string{}}
}

// Lines renders text to width columns. key names the text's owner; the cache
// also keys on a hash of the text, so an edited description re-renders.
func (r *Renderer) Lines(key, text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	ck := fmt.Sprintf("%s\x00%d\x00%x", key, width, sha256.Sum256([]byte(text)))
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.cache[ck]; ok {
		return l
	}
	l := r.render(text, width)
	r.cache[ck] = l
	return l
}

var numericEntity = regexp.MustCompile(`&#(?:[xX][0-9a-fA-F]+|[0-9]+);?`)

// withoutControlEntities drops character references that decode to control
// characters. The renderer decodes entities after the text was cleaned, so
// &#27;[8m would become a real "hide this text" escape that ScreenSafe cannot
// tell from the renderer's own styling.
func withoutControlEntities(s string) string {
	return numericEntity.ReplaceAllStringFunc(s, func(ref string) string {
		if strings.ContainsFunc(html.UnescapeString(ref), termsafe.IsControl) {
			return ""
		}
		return ref
	})
}

func (r *Renderer) render(text string, width int) []string {
	text = withoutControlEntities(text)
	cfg := styles.DarkStyleConfig
	if !r.dark {
		cfg = styles.LightStyleConfig
	}
	margin := uint(1)
	cfg.Document.Margin = &margin
	cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
	for _, h := range []*string{&cfg.H2.Prefix, &cfg.H3.Prefix, &cfg.H4.Prefix, &cfg.H5.Prefix, &cfg.H6.Prefix} {
		*h = ""
	}
	tr, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(max(10, width-2)))
	if err == nil {
		var out string
		if out, err = tr.Render(text); err == nil {
			return trimBlankEdges(strings.Split(termsafe.ScreenSafe(out), "\n"))
		}
	}
	return strings.Split(lipgloss.NewStyle().Width(max(10, width-1)).PaddingLeft(1).Render(text), "\n")
}

func trimBlankEdges(lines []string) []string {
	blank := func(s string) bool { return strings.TrimSpace(ansi.Strip(s)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}
