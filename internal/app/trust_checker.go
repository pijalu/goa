// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/skills"
)

// skillTrustChecker adapts trust.Manager to skills.TrustChecker.
type skillTrustChecker struct {
	manager *trust.Manager
}

// compile-time check.
var _ skills.TrustChecker = (*skillTrustChecker)(nil)

// IsTrusted returns true when the trust manager considers the skill trusted.
func (s *skillTrustChecker) IsTrusted(name, _ string) (bool, error) {
	if s.manager == nil {
		return true, nil
	}
	return s.manager.IsTrusted(name), nil
}

// newSkillTrustChecker wraps a trust manager for skill gating.
func newSkillTrustChecker(m *trust.Manager) skills.TrustChecker {
	return &skillTrustChecker{manager: m}
}
