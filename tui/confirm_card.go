// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"sync/atomic"

	"github.com/pijalu/goa/internal/ansi"
)

// ConfirmOption is one selectable row of a ConfirmCard.
type ConfirmOption struct {
	ID    string
	Label string
	// Style: "ok" | "danger" | "" (default). Semantic meaning resolved to
	// theme colors at render time ("token_critical" / "tool_success").
	Style string
	// Toggle marks a checkbox row (multi-select cards only): space flips its
	// checked state and Enter toggles it instead of delivering. Action rows
	// (Toggle=false) deliver the whole selection when picked.
	Toggle bool
	// DefaultOn seeds the checkbox state in multi-select cards; ignored in
	// single-select mode.
	DefaultOn bool
}

// ConfirmCard is the interactive modal behind goa.ui.confirm (plugins plan
// §4, Phase M3). It renders a title, an explanatory body, and a list of
// choices; arrow keys move, Enter picks, Esc/Ctrl+C dismisses.
//
// §9 Q3 resolution — confirm modal vs streaming contention: the card takes
// input focus like the existing selection popups (ShowSelector et al.,
// CaptureInput overlay + FocusStack), NOT the clarify-card model and NOT
// queue-until-idle. Rationale from reading today's behavior: ClarifyCard is
// display-only because its answer is FREE TEXT typed on the main editor
// ("Input discipline", docs/TUI.md); a confirm has no typing, so that model
// does not apply. Discrete choice already owns a focus-capturing precedent,
// the compositor renders capturing-overlay frames mid-stream safely
// (compositor_quota_stream_repro_test.go), and FocusStack restores the prior
// focus exactly on hide. Multiple queued confirms serialize FIFO — one card
// visible at a time (enforced by the app-side drain).
//
// Concurrency: the commandLoop is the sole owner of card state (HandleInput,
// Render, callbacks fire on it); SetFocused may arrive from ShowOverlay on
// the same loop. The atomic mirrors Selector's pattern defensively.
type ConfirmCard struct {
	title string
	body  string
	// rows is options plus the implicit Cancel row when allowCancel is set
	// ("default cancel appended by TUI", plan §4 step 1).
	rows        []ConfirmOption
	cancelRow   int // index of the implicit Cancel row, -1 when absent
	selected    int
	allowCancel bool

	// MultiSelect mode (M6 §7 step 3, plugin hook review): toggle rows carry
	// checkboxes; Enter on an action row delivers the checked IDs alongside
	// the action. checked maps option ID → state for Toggle rows.
	MultiSelect bool
	checked     map[string]bool

	choose func(id string, cancelled bool) // exactly one delivery, then Done

	done    func() // hides the overlay (wired to the OverlayHandle)
	focused atomic.Bool
}

// NewConfirmCard builds a confirm card. choose is invoked once with either
// (optionID, false) for a real choice or ("", true) for dismissal. Unknown
// defaultIDs select the first row.
func NewConfirmCard(title, body string, options []ConfirmOption, defaultID string, allowCancel bool, choose func(id string, cancelled bool)) *ConfirmCard {
	return newConfirmCard(title, body, options, defaultID, allowCancel, false, choose)
}

// NewMultiConfirmCard builds the multi-select variant (M6 §7 step 3):
// identical navigation and delivery contract as NewConfirmCard, plus space/
// Enter toggling of Toggle rows. Read SelectedIDs() from inside the choose
// callback to collect what was checked when an action row fired.
func NewMultiConfirmCard(title, body string, options []ConfirmOption, defaultID string, allowCancel bool, choose func(id string, cancelled bool)) *ConfirmCard {
	return newConfirmCard(title, body, options, defaultID, allowCancel, true, choose)
}

func newConfirmCard(title, body string, options []ConfirmOption, defaultID string, allowCancel bool, multi bool, choose func(id string, cancelled bool)) *ConfirmCard {
	rows := make([]ConfirmOption, len(options))
	copy(rows, options)
	checked := make(map[string]bool)
	for _, r := range rows {
		if r.Toggle && r.DefaultOn {
			checked[r.ID] = true
		}
	}
	cancelRow := -1
	if allowCancel {
		cancelRow = len(rows)
		rows = append(rows, ConfirmOption{ID: "", Label: "Cancel"})
	}
	c := &ConfirmCard{
		title:       strings.TrimSpace(title),
		body:        strings.TrimSpace(body),
		rows:        rows,
		cancelRow:   cancelRow,
		allowCancel: allowCancel,
		selected:    confirmRowIndex(rows, defaultID),
		MultiSelect: multi,
		checked:     checked,
		choose:      choose,
	}
	return c
}

// confirmRowIndex resolves the initial cursor: first row whose ID matches,
// else 0.
func confirmRowIndex(rows []ConfirmOption, defaultID string) int {
	if defaultID != "" {
		for i, r := range rows {
			if r.ID == defaultID {
				return i
			}
		}
	}
	return 0
}

// SetDone wires the hide callback (OverlayHandle.Hide) invoked before the
// choice is delivered, mirroring Selector.SetDone.
func (c *ConfirmCard) SetDone(fn func()) { c.done = fn }

// HasDanger reports whether any row uses the danger style (tests/goldens).
func (c *ConfirmCard) HasDanger() bool {
	for _, r := range c.rows {
		if r.Style == "danger" {
			return true
		}
	}
	return false
}

func (c *ConfirmCard) Focused() bool     { return c.focused.Load() }
func (c *ConfirmCard) SetFocused(f bool) { c.focused.Store(f) }
func (c *ConfirmCard) Invalidate()       {}

// HandleInput processes navigation/selection keys. Runs on the commandLoop
// (sole owner). Unrecognized keys are consumed silently: while a destructive
// prompt is on screen, stray input must never leak into the editor below.
func (c *ConfirmCard) HandleInput(data string) {
	switch {
	case matchesKey(data, KeyUp) || data == "k":
		c.move(-1)
	case matchesKey(data, KeyDown) || data == "j":
		c.move(1)
	case c.MultiSelect && data == " ":
		c.toggleRow(c.selected)
	case matchesKey(data, KeyEnter):
		c.pick(c.selected)
	case matchesKey(data, KeyEscape) || matchesKey(data, KeyCtrlC):
		if c.allowCancel {
			c.pick(c.cancelRow)
		}
		// Without AllowCancel the request forces a real choice; Esc is
		// consumed and ignored.
	}
}

// move shifts the cursor with wrap-around.
func (c *ConfirmCard) move(delta int) {
	n := len(c.rows)
	if n == 0 {
		return
	}
	c.selected = ((c.selected+delta)%n + n) % n
}

// toggleRow flips the checkbox of the row under the cursor in multi-select
// mode; action rows and the implicit Cancel row are unaffected.
func (c *ConfirmCard) toggleRow(idx int) {
	row := c.rows[idx]
	if !row.Toggle || row.ID == "" {
		return
	}
	c.checked[row.ID] = !c.checked[row.ID]
}

// pick delivers the row under the cursor. In multi-select mode Enter on a
// Toggle row toggles its checkbox instead of delivering; action rows deliver
// once with the current selection readable via SelectedIDs(). The implicit
// Cancel row and the explicit dismissal paths both report cancelled=true.
func (c *ConfirmCard) pick(idx int) {
	if idx < 0 || idx >= len(c.rows) || c.choose == nil {
		return
	}
	row := c.rows[idx]
	if c.MultiSelect && row.Toggle {
		c.toggleRow(idx)
		return
	}
	cancelled := idx == c.cancelRow
	if c.done != nil {
		c.done()
	}
	c.choose(row.ID, cancelled)
}

// SelectedIDs returns the checked toggle-row IDs in display order. Callers
// read it from inside the choose callback — after that the card is done.
func (c *ConfirmCard) SelectedIDs() []string {
	var ids []string
	for _, r := range c.rows {
		if r.Toggle && c.checked[r.ID] {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// confirmColors collects theme-derived ANSI prefixes. All lookups tolerate
// missing tokens ("" ⇒ unstyled), mirroring cardColors in clarify_card.go.
type confirmColors struct {
	bd, ac, dim, danger, ok string
}

func newConfirmColors() confirmColors {
	bd, ac, dim := cardColors()
	return confirmColors{
		bd:     bd,
		ac:     ac,
		dim:    dim,
		danger: themeFgOr("token_critical"),
		ok:     themeFgOr("tool_success"),
	}
}

// themeFgOr resolves a theme token to an ANSI fg prefix, "" when absent.
func themeFgOr(token string) string {
	hex := TheTheme.ColorHex(token)
	if hex == "" {
		return ""
	}
	return ansi.Fg(hex)
}

// Render draws the bordered modal: accent title, wrapped body, the option
// list with cursor, and a key hint footer. Every line is padded to the full
// width (overlay placement replaces whole canvas rows).
func (c *ConfirmCard) Render(width int) []string {
	const minW, maxW = 46, 64
	if width < minW {
		width = minW
	}
	if width > maxW {
		width = maxW
	}
	col := newConfirmColors()
	reset := ansi.Reset
	inner := width - 4 // border + leading space each side

	body := c.renderBody(inner, col)
	top := col.bd + ansi.BoxRoundedTopLeft + strings.Repeat(ansi.BoxHorizontal, width-2) + ansi.BoxRoundedTopRight + reset
	bot := col.bd + ansi.BoxRoundedBottomLeft + strings.Repeat(ansi.BoxHorizontal, width-2) + ansi.BoxRoundedBottomRight + reset
	cellW := width - 2

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, padToWidthStyled(top, width, ""))
	for _, raw := range body {
		lines = append(lines, col.bd+ansi.BoxVertical+reset+padToWidthStyled(" "+raw, cellW, "")+col.bd+ansi.BoxVertical+reset)
	}
	lines = append(lines, padToWidthStyled(bot, width, ""))
	return lines
}

// renderBody builds the inner content lines at the given inner width.
func (c *ConfirmCard) renderBody(inner int, col confirmColors) []string {
	reset := ansi.Reset
	var body []string

	title := c.title
	if title == "" {
		title = "Confirm"
	}
	for _, l := range ansi.Wrap("❓ "+title, inner) {
		body = append(body, col.ac+l+reset)
	}
	if c.body != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, wrapStyled(c.body, inner, "%s")...)
	}
	if len(c.rows) > 0 {
		body = append(body, "")
	}
	for i, row := range c.rows {
		body = append(body, c.renderRow(i, row, col, reset))
	}
	body = append(body, "", c.renderHint(col, reset))
	return body
}

// renderRow formats one option line: cursor marker plus style-colored label.
// Selected rows use Selector's accent treatment ("› " in success color);
// danger labels carry the critical color in BOTH states so the irreversible
// choice reads at a glance.
func (c *ConfirmCard) renderRow(i int, row ConfirmOption, col confirmColors, reset string) string {
	label := row.Label
	prefix := "  "
	if i != c.selected {
		prefix = ""
	}
	if c.MultiSelect && row.Toggle {
		box := "[ ] "
		if c.checked[row.ID] {
			box = "[x] "
		}
		label = box + label
	}
	if i == c.selected {
		cursor := ansi.Fg(col.ok) + "› " + reset
		switch row.Style {
		case "danger":
			return cursor + col.danger + label + reset
		case "ok":
			return cursor + col.ok + label + reset
		default:
			return cursor + ansi.Fg(TheTheme.ColorHex("assistant_msg")) + label + reset
		}
	}
	switch row.Style {
	case "danger":
		return prefix + col.danger + ansi.Faint + label + reset
	case "ok":
		return prefix + col.ok + ansi.Faint + label + reset
	default:
		return prefix + ansi.Fg(col.dim) + ansi.Faint + label + reset
	}
}

// renderHint builds the faint key legend; the esc fragment appears only when
// cancellation is allowed.
func (c *ConfirmCard) renderHint(col confirmColors, reset string) string {
	hint := "↑↓ move · enter select"
	if c.MultiSelect {
		hint += " · space toggle"
	}
	if c.allowCancel {
		hint += " · esc cancel"
	}
	return ansi.Fg(col.dim) + ansi.Faint + hint + reset
}
