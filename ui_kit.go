package main

// ui_kit.go — small, dependency-free building blocks for the TUI.
//
// Everything here is a pure function of its arguments (plus the current
// theme palette): no model access, no I/O. That keeps the geometry code easy
// to unit-test. The key design rule is that *we* own layout: every panel and
// row is built to an exact width/height using ANSI-aware clip/pad helpers, so
// the screen can never overflow or wrap regardless of terminal size, theme
// or script output.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// ─── colour palette ─────────────────────────────────────────────────────────

// palette is derived from the active theme. Neutral text colours are fixed
// (chosen for readable contrast on dark terminals); every other colour is
// computed from the theme so all presets look coherent.
type palette struct {
	accent, success, fail, stopped, idle   string
	text, muted, dim                       string
	border, borderFocus, surface           string
	onAccent, onSuccess, onFail, onStopped string
}

var pal = buildPalette(themePresets[0])

func rebuildPalette() { pal = buildPalette(activeTheme) }

func buildPalette(t ThemeConfig) palette {
	if t.Accent == "" {
		t = themePresets[0]
	}
	p := palette{
		accent:  t.Accent,
		success: t.Success,
		fail:    t.Fail,
		stopped: t.Stopped,
		idle:    t.Idle,
		text:    "#e5e7eb",
		muted:   "#9ca3af",
		dim:     "#6b7280",
	}
	// Several presets use an Idle colour that is nearly black; mixing it with
	// a mid grey keeps unfocused borders visible on every theme.
	p.border = mixHex(t.Idle, "#9ca3af", 0.30)
	p.borderFocus = t.Accent
	// Selected-row / chip background: dark base tinted with the accent.
	p.surface = mixHex("#111827", t.Accent, 0.22)
	p.onAccent = readableOn(t.Accent)
	p.onSuccess = readableOn(t.Success)
	p.onFail = readableOn(t.Fail)
	p.onStopped = readableOn(t.Stopped)
	return p
}

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

// mixHex blends colour a towards colour b by t (0 = a, 1 = b). If either input
// is not a hex colour, a is returned unchanged.
func mixHex(a, b string, t float64) string {
	ar, ag, ab, ok1 := parseHex(a)
	br, bg, bb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return a
	}
	mix := func(x, y float64) int { return int(math.Round(x + (y-x)*t)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

func relLuminance(r, g, b float64) float64 {
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// readableOn returns a foreground colour with good contrast on bg. Light
// backgrounds (e.g. the neon "matrix" green) get dark text instead of the
// white text that would be unreadable on them.
func readableOn(bg string) string {
	r, g, b, ok := parseHex(bg)
	if !ok {
		return "#ffffff"
	}
	if relLuminance(r, g, b) > 0.28 {
		return "#0b0f14"
	}
	return "#ffffff"
}

// ─── styling shorthands ─────────────────────────────────────────────────────

func paint(s, fg string) string { return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Render(s) }
func paintB(s, fg string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Bold(true).Render(s)
}
func tText(s string) string  { return paint(s, pal.text) }
func tMuted(s string) string { return paint(s, pal.muted) }
func tDim(s string) string   { return paint(s, pal.dim) }

func chip(text, fg, bg string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(fg)).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Render(" " + text + " ")
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// ─── ANSI-aware width / clip / pad ──────────────────────────────────────────

// ansiLen returns the byte length of the escape sequence that starts at s[i]
// (s[i] must be ESC). Incomplete sequences consume the rest of the string.
func ansiLen(s string, i int) int {
	if i+1 >= len(s) {
		return len(s) - i
	}
	switch s[i+1] {
	case '[': // CSI: params/intermediates, then a final byte 0x40–0x7e
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j - i
	case ']': // OSC: terminated by BEL or ST (ESC \)
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1 - i
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2 - i
			}
			j++
		}
		return len(s) - i
	default:
		return 2
	}
}

// runeCells is the terminal cell width of a single rune.
func runeCells(r rune) int {
	if r < 0x80 {
		if r < 0x20 || r == 0x7f {
			return 0
		}
		return 1
	}
	return lipgloss.Width(string(r))
}

func visibleWidth(s string) int { return lipgloss.Width(s) }

// clipANSI returns s cut to at most w visible cells, keeping escape sequences
// intact. If it truncates a styled string it appends a reset so colour never
// bleeds into whatever is drawn next.
func clipANSI(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	used, i := 0, 0
	sawEsc := false
	for i < len(s) {
		if s[i] == 0x1b {
			n := ansiLen(s, i)
			b.WriteString(s[i : i+n])
			sawEsc = true
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeCells(r)
		if used+rw > w {
			break
		}
		b.WriteString(s[i : i+size])
		used += rw
		i += size
	}
	out := b.String()
	if sawEsc && i < len(s) {
		out += "\x1b[0m"
	}
	return out
}

// fit clips or pads s to exactly w visible cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = clipANSI(s, w)
	if strings.IndexByte(s, 0x1b) >= 0 {
		s += "\x1b[0m" // never let a script's colour leak past this cell
	}
	if n := visibleWidth(s); n < w {
		s += spaces(w - n)
	}
	return s
}

// truncate shortens a PLAIN (unstyled) string to w cells with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeCells(r)
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// oneLine replaces control characters (tabs, newlines, …) in user-supplied
// text with spaces so it can never break a single-line cell.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// spread lays out left and right text in exactly w cells, padding between
// them with spaces. The right side wins when space is short; the left side is
// clipped.
func spread(left, right string, w int) string {
	lw, rw := visibleWidth(left), visibleWidth(right)
	switch {
	case lw+rw+1 <= w:
		return left + spaces(w-lw-rw) + right
	case rw+2 <= w:
		return fit(left, w-rw-1) + " " + right
	default:
		return fit(left, w)
	}
}

func centerText(s string, w int) string {
	sw := visibleWidth(s)
	if sw >= w {
		return fit(s, w)
	}
	l := (w - sw) / 2
	return spaces(l) + s + spaces(w-sw-l)
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ─── scrolling windows ──────────────────────────────────────────────────────

// scrollOffset returns the new first-visible index for a list so the cursor is
// always on screen, moving as little as possible (like a text editor).
func scrollOffset(offset, total, visible, cursor int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	cursor = clampInt(cursor, 0, total-1)
	if cursor < offset {
		offset = cursor
	} else if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	return clampInt(offset, 0, total-visible)
}

// window returns [start,end) so that cursor is visible, roughly centred.
func window(total, visible, cursor int) (int, int) {
	if visible <= 0 || total <= 0 {
		return 0, 0
	}
	if total <= visible {
		return 0, total
	}
	cursor = clampInt(cursor, 0, total-1)
	start := clampInt(cursor-visible/2, 0, total-visible)
	return start, start + visible
}

// ─── joining blocks of lines ────────────────────────────────────────────────

// hjoin places blocks of lines side by side. Blocks are padded to their own
// widest line and to the tallest block's height.
func hjoin(blocks ...[]string) []string {
	h := 0
	widths := make([]int, len(blocks))
	for i, b := range blocks {
		if len(b) > h {
			h = len(b)
		}
		for _, l := range b {
			if w := visibleWidth(l); w > widths[i] {
				widths[i] = w
			}
		}
	}
	out := make([]string, h)
	for r := 0; r < h; r++ {
		var sb strings.Builder
		for i, b := range blocks {
			l := ""
			if r < len(b) {
				l = b[r]
			}
			sb.WriteString(l)
			if w := visibleWidth(l); w < widths[i] {
				sb.WriteString(spaces(widths[i] - w))
			}
		}
		out[r] = sb.String()
	}
	return out
}

// ─── panels ─────────────────────────────────────────────────────────────────

type panelSpec struct {
	title   string // pre-styled, shown top-left
	right   string // pre-styled, shown top-right
	focused bool
	color   string // optional border colour override
}

// renderPanel draws a rounded box of exactly w×h cells with the title embedded
// in the top rule. body lines are clipped/padded to the inner width; missing
// lines are blank. Guaranteed: len(result)==h and every line is w cells wide.
func renderPanel(w, h int, spec panelSpec, body []string) []string {
	if h <= 0 {
		return nil
	}
	if w < 4 || h < 2 {
		out := make([]string, h)
		for i := range out {
			out[i] = spaces(w)
		}
		return out
	}
	bc := spec.color
	if bc == "" {
		bc = pal.border
		if spec.focused {
			bc = pal.borderFocus
		}
	}
	bs := lipgloss.NewStyle().Foreground(lipgloss.Color(bc))
	inner := w - 2

	title, right := spec.title, spec.right
	tw, rw := 0, 0
	if title != "" {
		title = " " + title + " "
		tw = visibleWidth(title)
	}
	if right != "" {
		right = " " + right + " "
		rw = visibleWidth(right)
	}
	fill := inner - 2 - tw - rw
	if fill < 0 { // not enough room: drop the right label first
		right, rw = "", 0
		fill = inner - 2 - tw
	}
	if fill < 0 { // still too long: clip the title
		title = clipANSI(title, inner-2)
		tw = visibleWidth(title)
		fill = inner - 2 - tw
		if fill < 0 {
			fill = 0
		}
	}
	top := bs.Render("╭─") + title + bs.Render(strings.Repeat("─", fill)) + right + bs.Render("─╮")

	rows, cw := h-2, w-4
	vbar := bs.Render("│")
	lines := make([]string, 0, h)
	lines = append(lines, top)
	for i := 0; i < rows; i++ {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		lines = append(lines, vbar+" "+fit(l, cw)+" "+vbar)
	}
	lines = append(lines, bs.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return lines
}

// ─── background, ANSI stripping, overlays ───────────────────────────────────

// bgOnSeq returns the escape sequence that switches the background to colour
// c under the current colour profile ("" when the terminal has no colour).
func bgOnSeq(c string) string {
	out := lipgloss.NewStyle().Background(lipgloss.Color(c)).Render("X")
	if i := strings.IndexByte(out, 'X'); i > 0 {
		return out[:i]
	}
	return ""
}

// bgWrap pads line to exactly w cells and paints the whole row with
// background colour bg. Inner styles reset the background whenever they end,
// so it is re-applied after every reset.
func bgWrap(line string, w int, bg string) string {
	line = fit(line, w)
	on := bgOnSeq(bg)
	if on == "" {
		return line
	}
	return on + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on) + "\x1b[0m"
}

// stripANSI removes every escape sequence from s.
func stripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += ansiLen(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// sliceCells returns the part of the PLAIN string s that covers cells
// [from, to). Wide runes that straddle a boundary are replaced by spaces.
func sliceCells(s string, from, to int) string {
	var b strings.Builder
	pos := 0
	for _, r := range s {
		rw := runeCells(r)
		if rw == 0 {
			continue
		}
		start, end := pos, pos+rw
		pos = end
		switch {
		case end <= from || start >= to:
		case start >= from && end <= to:
			b.WriteRune(r)
		default:
			b.WriteString(spaces(min(end, to) - max(start, from)))
		}
		if pos >= to {
			break
		}
	}
	return b.String()
}

// dimBackdrop mutes a rendered screen so a dialog stands out above it.
func dimBackdrop(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = tDim(fit(stripANSI(l), w))
	}
	return out
}

// overlay draws panel over base at (top, left). Base is dimmed and flattened to
// plain text first, so the splice never has to cut through colour sequences.
func overlay(base []string, w int, panel []string, top, left int) []string {
	out := dimBackdrop(base, w)
	pw := 0
	for _, l := range panel {
		pw = max(pw, visibleWidth(l))
	}
	for i, pl := range panel {
		r := top + i
		if r < 0 || r >= len(out) {
			continue
		}
		plain := fit(stripANSI(base[r]), w)
		out[r] = tDim(sliceCells(plain, 0, left)) + fit(pl, pw) + tDim(sliceCells(plain, left+pw, w))
	}
	return out
}

// ─── modal dialogs ──────────────────────────────────────────────────────────

type modalSpec struct {
	title    string // plain text
	subtitle string // optional, plain text, right side of the title bar
	wantW    int    // desired outer width
	danger   bool
}

// modalFrame builds a dialog panel and returns it with its screen position.
// body is separated from footer by a thin rule. Extra body lines are dropped
// from the bottom if the dialog would not fit (callers should window first).
func modalFrame(termW, termH int, spec modalSpec, body, footer []string) (panel []string, top, left int) {
	pw := clampInt(spec.wantW, 30, termW-2)
	maxBody := termH - 2 - len(footer) - 1
	if maxBody < 1 {
		maxBody = 1
	}
	if len(body) > maxBody {
		body = body[:maxBody]
	}
	color, tcolor := pal.accent, pal.text
	if spec.danger {
		color, tcolor = pal.fail, pal.fail
	}
	cw := pw - 4
	content := make([]string, 0, len(body)+1+len(footer))
	content = append(content, body...)
	content = append(content, tDim(strings.Repeat("─", cw)))
	content = append(content, footer...)

	ph := len(content) + 2
	right := ""
	if spec.subtitle != "" {
		right = tMuted(truncate(spec.subtitle, pw/2))
	}
	panel = renderPanel(pw, ph, panelSpec{
		title: paintB(truncate(spec.title, pw-8), tcolor),
		right: right,
		color: color,
	}, content)
	return panel, max(0, (termH-ph)/2), max(0, (termW-pw)/2)
}

// button renders a dialog button; focused buttons are filled.
func button(label string, focused, danger bool) string {
	if !focused {
		return chip(label, pal.muted, pal.surface)
	}
	if danger {
		return chip(label, pal.onFail, pal.fail)
	}
	return chip(label, pal.onAccent, pal.accent)
}

// ─── key hints ──────────────────────────────────────────────────────────────

type hint struct {
	key, desc string
	prio      int // 0 = always shown; larger = dropped earlier on narrow terminals
}

// renderHints builds a single line of "key desc" pairs that is guaranteed to
// fit in w cells: lowest-priority hints are removed first.
func renderHints(w int, hs []hint) string {
	keep := make([]bool, len(hs))
	for i := range keep {
		keep[i] = true
	}
	total := func() int {
		sum, n := 1, 0
		for i, h := range hs {
			if keep[i] {
				sum += visibleWidth(h.key) + 1 + visibleWidth(h.desc)
				n++
			}
		}
		if n > 1 {
			sum += (n - 1) * 2
		}
		return sum
	}
	for total() > w {
		best := -1
		for i, h := range hs {
			if keep[i] && h.prio > 0 && (best < 0 || h.prio >= hs[best].prio) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		keep[best] = false
	}
	var parts []string
	for i, h := range hs {
		if keep[i] {
			parts = append(parts, paintB(h.key, pal.accent)+" "+tMuted(h.desc))
		}
	}
	return fit(" "+strings.Join(parts, "  "), w)
}

// ─── status messages ────────────────────────────────────────────────────────

type statusLevel int

const (
	levelInfo statusLevel = iota
	levelSuccess
	levelError
)

// classifyStatus infers a severity from the message text so the status line
// can colour errors red and confirmations green without every call site
// having to say so.
func classifyStatus(msg string) statusLevel {
	l := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(l, "error"),
		strings.Contains(l, "failed"),
		strings.Contains(l, "cannot"),
		strings.Contains(l, "invalid"),
		strings.Contains(l, "already exists"),
		strings.Contains(l, "already used"),
		strings.Contains(l, "not writable"):
		return levelError
	case strings.Contains(l, "successfully"),
		strings.Contains(l, " saved"),
		strings.Contains(l, " updated"),
		strings.Contains(l, " deleted"),
		strings.Contains(l, "copied"),
		strings.Contains(l, "complete"),
		strings.Contains(l, "switched"),
		strings.HasPrefix(l, "started"),
		strings.HasPrefix(l, "loaded"):
		return levelSuccess
	}
	return levelInfo
}

func statusColor(l statusLevel) (glyph, color string) {
	switch l {
	case levelError:
		return "✕", pal.fail
	case levelSuccess:
		return "✓", pal.success
	}
	return "●", pal.accent
}

// ─── run state visuals ──────────────────────────────────────────────────────

func spinnerFrame() string {
	return spinnerFrames[int(time.Now().UnixMilli()/int64(frameInterval/time.Millisecond))%len(spinnerFrames)]
}

// stateVisual maps a script state to its glyph, colour and label.
func stateVisual(state string, queued bool) (glyph, color, label string) {
	switch state {
	case "Running":
		return spinnerFrame(), pal.accent, "RUNNING"
	case "Success":
		return "✓", pal.success, "SUCCESS"
	case "Failed":
		return "✕", pal.fail, "FAILED"
	case "Stopped":
		return "⊘", pal.stopped, "STOPPED"
	}
	if queued {
		return "◔", pal.muted, "QUEUED"
	}
	if state == "" || state == "Idle" {
		return "○", pal.dim, "IDLE"
	}
	return "?", pal.dim, strings.ToUpper(state)
}

// ─── progress bar & sparkline ───────────────────────────────────────────────

// Progress-bar glyphs from config (Settings popup). Kept as package variables
// because model.go loads and updates them.
var (
	progressCompletedChar = legacyFill
	progressPendingChar   = legacyTrack
)

// Legacy default glyphs: when the config still has these the modern thin bar is
// used; any other configured glyphs are honoured as-is.
const (
	legacyFill  = "▮"
	legacyTrack = "▯"
)

func progressBar(w, pct int, color string) string {
	return progressBarWith(w, pct, color, progressCompletedChar, progressPendingChar)
}

func progressBarWith(w, pct int, color, fill, track string) string {
	if w <= 0 {
		return ""
	}
	pct = clampInt(pct, 0, 100)
	if fill != "" && track != "" && !(fill == legacyFill && track == legacyTrack) {
		n := pct * w / 100
		return paint(strings.Repeat(fill, n), color) + paint(strings.Repeat(track, w-n), pal.dim)
	}
	units := pct * w * 2 / 100 // half-cell precision
	full, half := units/2, units%2
	lit := strings.Repeat("━", full)
	if half == 1 {
		lit += "╸"
	}
	rest := w - full - half
	return paint(lit, color) + paint(strings.Repeat("─", rest), pal.dim)
}

// indeterminateBar animates a sliding segment, used for running scripts that
// do not report __PROGRESS__ so they don't look frozen.
func indeterminateBar(w int, color string) string {
	if w <= 0 {
		return ""
	}
	segLen := clampInt(w/5, 3, 12)
	pos := int(time.Now().UnixMilli()/70) % (w + segLen)
	start := clampInt(pos-segLen, 0, w)
	end := clampInt(pos, 0, w)
	return paint(strings.Repeat("─", start), pal.dim) +
		paint(strings.Repeat("━", end-start), color) +
		paint(strings.Repeat("─", w-end), pal.dim)
}

var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// sparkline renders the most recent samples as block characters in exactly w
// cells. The scale is the larger of the observed maximum and floor, so small
// fluctuations don't look dramatic.
func sparkline(samples []float64, w int, floor float64, color string) string {
	if w <= 0 {
		return ""
	}
	vis := samples
	if len(vis) > w {
		vis = vis[len(vis)-w:]
	}
	ceil := floor
	for _, v := range vis {
		if v > ceil {
			ceil = v
		}
	}
	if ceil <= 0 {
		ceil = 1
	}
	var run strings.Builder
	for _, v := range vis {
		r := math.Max(0, math.Min(1, v/ceil))
		run.WriteRune(sparkBlocks[int(math.Round(r*float64(len(sparkBlocks)-1)))])
	}
	return paint(strings.Repeat("▁", w-len(vis)), pal.dim) + paint(run.String(), color)
}

// ─── human-friendly formatting ──────────────────────────────────────────────

func humanAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 21*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

func formatMem(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", mb/1024)
	}
	return fmt.Sprintf("%.0fM", mb)
}

var secretKeyHints = []string{"pass", "secret", "token", "key", "pwd", "credential", "auth", "private"}

// isSecretKey reports whether an environment variable name looks sensitive.
func isSecretKey(k string) bool {
	l := strings.ToLower(k)
	for _, h := range secretKeyHints {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

func sortedInputKeys(in map[string]interface{}) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ─── log hygiene ────────────────────────────────────────────────────────────

// sanitizeLog makes script output safe to draw inside the UI. Scripts run with
// FORCE_COLOR/TERM set, so tools freely emit cursor-movement, erase and
// private-mode sequences that would corrupt the screen. Colour (SGR) sequences
// and newlines are kept; tabs become spaces; everything else is dropped.
func sanitizeLog(s string) string {
	dirty := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\n') || c == 0x7f {
			dirty = true
			break
		}
	}
	if !dirty {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			n := ansiLen(s, i)
			if seq := s[i : i+n]; len(seq) >= 3 && seq[1] == '[' && seq[len(seq)-1] == 'm' {
				b.WriteString(seq)
			}
			i += n
		case c == '\t':
			b.WriteString("    ")
			i++
		case c == '\n':
			b.WriteByte('\n')
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

var benignErrorPhrases = []string{
	"0 error", "no error", "without error", "errors: 0", "error: 0",
	"0 fail", "no fail", "failed: 0", "failures: 0", "0 warn", "no warn",
}

// colorizeLogLine highlights obvious error / warning lines so problems stand
// out while scanning. Lines that already carry their own colours are left alone.
func colorizeLogLine(line string) string {
	if line == "" || strings.IndexByte(line, 0x1b) >= 0 {
		return line
	}
	l := strings.ToLower(line)
	for _, p := range benignErrorPhrases {
		if strings.Contains(l, p) {
			return line
		}
	}
	for _, k := range []string{"error", "fatal", "panic", "exception", "traceback", "failed", "failure"} {
		if strings.Contains(l, k) {
			return paint(line, pal.fail)
		}
	}
	if strings.Contains(l, "warn") {
		return paint(line, pal.stopped)
	}
	return line
}
