package tui

import (
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
)

// markdownCache holds the last rendered body. previewLines runs on every clamp,
// move and frame, so rendering must not repeat while ref/body/width/theme hold.
type markdownCache struct {
	ref, body   string
	width       int
	dark, color bool
	lines       []string
	rendered    bool
}

// renderBody renders sanitized issue markdown for display. External text is
// stripped of control sequences first; the renderer's output is then reduced to
// SGR styling only, so no hyperlink (OSC 8) or other terminal control from the
// renderer reaches the screen. Opening a URL stays the explicit o action.
func (m *app) renderBody(ref, body string, width int) []string {
	c := &m.markdown
	if c.rendered && c.ref == ref && c.body == body && c.width == width && c.dark == m.dark && c.color == m.color {
		return c.lines
	}
	lines, ok := renderMarkdown(model.Plain(body), width, m.dark, m.color)
	if !ok {
		lines = wrapped(body, width)
	}
	*c = markdownCache{ref: ref, body: body, width: width, dark: m.dark, color: m.color, lines: lines, rendered: true}
	return lines
}

func renderMarkdown(body string, width int, dark, color bool) ([]string, bool) {
	if width < 8 {
		return nil, false
	}
	style := styles.ASCIIStyleConfig
	if color {
		style = styles.LightStyleConfig
		if dark {
			style = styles.DarkStyleConfig
		}
	}
	zero := uint(0)
	style.Document.Margin = &zero
	style.Document.BlockPrefix, style.Document.BlockSuffix = "", ""
	style.CodeBlock.Margin = &zero
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width), glamour.WithTableWrap(true), glamour.WithInlineTableLinks(true), glamour.WithPreservedNewLines()) // GitHub/GitLab issue bodies treat single newlines as breaks
	if err != nil {
		return nil, false
	}
	out, err := r.Render(body)
	if err != nil {
		return nil, false
	}
	out = strings.Trim(sgrOnly(out), "\n")
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		// The renderer pads every line with styled spaces; cut to the visible text.
		visible := ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " "))
		lines[i] = dropEmptySGR(ansi.Cut(line, 0, visible))
	}
	return lines, true
}

// sgrOnly keeps printable text, newlines and CSI ... m (SGR) sequences. Every
// other escape (OSC hyperlinks, cursor movement, DCS/APC strings) and C0/C1
// control is dropped.
func sgrOnly(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' && validSGR(s[i+2:j]) {
				out.WriteString(s[i : j+1])
			}
			i = j + 1
		case c == 0x1b && i+1 < len(s) && (s[i+1] == ']' || s[i+1] == 'P' || s[i+1] == '_' || s[i+1] == '^' || s[i+1] == 'X'):
			// String sequence: skip through BEL or ST.
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j + 1
		case c == 0x1b:
			i += 2
		case c == '\n':
			out.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			if c == '\t' {
				out.WriteString("    ")
			}
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return keepC1Free(out.String())
}

var emptySGR = regexp.MustCompile(`\x1b\[[0-9;:]*m(\x1b\[0?m)`)

// dropEmptySGR removes style sequences that are immediately reset (no text).
func dropEmptySGR(s string) string {
	for {
		next := emptySGR.ReplaceAllString(s, "$1")
		next = strings.ReplaceAll(next, "\x1b[m\x1b[m", "\x1b[m")
		if next == s {
			return s
		}
		s = next
	}
}

func validSGR(params string) bool {
	for _, r := range params {
		if (r < '0' || r > '9') && r != ';' && r != ':' {
			return false
		}
	}
	return true
}

// keepC1Free removes 8-bit C1 controls (U+0080–U+009F), e.g. a single-byte CSI.
func keepC1Free(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x80 && r <= 0x9f {
			return -1
		}
		return r
	}, s)
}
