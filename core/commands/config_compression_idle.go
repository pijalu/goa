// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"

	"github.com/pijalu/goa/tui"
)

func (m *configMenu) settingCompressionIdleTrigger() {
	m.current = m.settingCompressionIdleTrigger
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.IdleSummarize.TriggerPercentValue())
	m.ctx.SelectOption("Idle summarize minimum context occupancy:", idleSummarizeTriggerItems(false), current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.idle_summarize.trigger_percent", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionIdleDelay() {
	m.current = m.settingCompressionIdleDelay
	current := m.ctx.Config.ContextCompression.IdleSummarize.DelaySecondsValue()
	m.ctx.SelectOption("Idle summarize delay (seconds; 0 = disabled):", idleSummarizeDelayItems(false), fmt.Sprintf("%d", current), func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.idle_summarize.delay_seconds", v)
		m.back()
	})
}

// idleSummarizeTriggerItems is shared by global and per-model pickers. The
// per-model variant prepends an explicit clear-to-inherit row.
func idleSummarizeTriggerItems(withInherit bool) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, 22)
	if withInherit {
		items = append(items, tui.SelectorItem{Value: perModelInheritValue, Label: "inherit (clear)", Description: "use the global idle trigger", PreserveOrder: true})
	}
	for percent := 0; percent <= 100; percent += 5 {
		description := "compact at or above this occupancy"
		if percent == 0 {
			description = "allow any non-empty history"
		}
		items = append(items, tui.SelectorItem{
			Value: fmt.Sprintf("%d", percent), Label: fmt.Sprintf("%d%%", percent), Description: description, PreserveOrder: true,
		})
	}
	return items
}

// idleSummarizeDelayItems is shared by global and per-model pickers. Zero is
// an actual override (disabled), distinct from the optional inheritance row.
func idleSummarizeDelayItems(withInherit bool) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, 9)
	if withInherit {
		items = append(items, tui.SelectorItem{Value: perModelInheritValue, Label: "inherit (clear)", Description: "use the global idle delay", PreserveOrder: true})
	}
	for _, seconds := range []int{0, 30, 60, 120, 240, 300, 600, 900} {
		items = append(items, tui.SelectorItem{
			Value: fmt.Sprintf("%d", seconds), Label: idleSummarizeDelayLabel(seconds), Description: "inactivity after a completed stop", PreserveOrder: true,
		})
	}
	return items
}

func idleSummarizeDelayLabel(seconds int) string {
	if seconds == 0 {
		return "off"
	}
	return fmt.Sprintf("%ds", seconds)
}

func perModelIdleTriggerLabel(value *int) string {
	if value == nil {
		return "inherit"
	}
	return fmt.Sprintf("%d%%", *value)
}

func perModelIdleDelayLabel(value *int) string {
	if value == nil {
		return "inherit"
	}
	return idleSummarizeDelayLabel(*value)
}
