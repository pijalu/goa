// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import "strings"

func parseBool(value string) bool {
	switch strings.ToLower(value) {
	case "true", "on", "1", "yes":
		return true
	default:
		return false
	}
}
func boolPtrValue(v *bool) bool { return v != nil && *v }
func parseToggle(value string, inverted bool) bool {
	v := parseBool(value)
	if isOnOff(value) {
		v = strings.ToLower(value) == "on"
	}
	if inverted {
		return !v
	}
	return v
}
func isOnOff(value string) bool {
	switch strings.ToLower(value) {
	case "on", "off":
		return true
	default:
		return false
	}
}
