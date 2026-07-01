// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestNewSessionState(t *testing.T) {
	defaults := internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo}
	s := NewSessionState(defaults)

	got := s.Current()
	if got.Major != internal.MajorCoder {
		t.Errorf("Current().Major = %q, want %q", got.Major, internal.MajorCoder)
	}
	if got.Autonomy != internal.AutonomyYolo {
		t.Errorf("Current().Autonomy = %q, want %q", got.Autonomy, internal.AutonomyYolo)
	}
}

func TestSessionState_Current_ZeroDefaults(t *testing.T) {
	s := NewSessionState(internal.ModeState{})
	got := s.Current()
	if !got.IsZero() {
		t.Errorf("Current().IsZero() = false, want true")
	}
}

func TestSessionState_SetMode(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})

	updated := s.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	// SetMode returns the new mode
	if updated.Major != internal.MajorPlanner {
		t.Errorf("SetMode returned Major = %q, want %q", updated.Major, internal.MajorPlanner)
	}

	// Current reflects the change
	got := s.Current()
	if got.Major != internal.MajorPlanner {
		t.Errorf("Current().Major = %q, want %q", got.Major, internal.MajorPlanner)
	}
}

func TestSessionState_PushMode_SavesPrevious(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})

	prev := s.PushMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview}, "skill: planner")

	// PushMode returns the previous mode
	if prev.Major != internal.MajorCoder {
		t.Errorf("PushMode returned prev.Major = %q, want %q", prev.Major, internal.MajorCoder)
	}

	// Current is now the pushed mode
	got := s.Current()
	if got.Major != internal.MajorPlanner {
		t.Errorf("Current().Major = %q, want %q", got.Major, internal.MajorPlanner)
	}

	// Mode source is set
	if s.Source() != "skill: planner" {
		t.Errorf("Source = %q, want %q", s.Source(), "skill: planner")
	}
}

func TestSessionState_PopMode_RestoresPrevious(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})

	// Push a new mode
	s.PushMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview}, "skill: planner")

	// Pop restores the original
	restored := s.PopMode()
	if restored.Major != internal.MajorCoder {
		t.Errorf("PopMode restored.Major = %q, want %q", restored.Major, internal.MajorCoder)
	}

	// Current is now restored
	got := s.Current()
	if got.Major != internal.MajorCoder {
		t.Errorf("Current().Major = %q, want %q", got.Major, internal.MajorCoder)
	}
}

func TestSessionState_PushPop_Nested(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: "base"})

	// push A → push B → pop → pop restores original
	s.PushMode(internal.ModeState{Major: "A"}, "source-a")
	s.PushMode(internal.ModeState{Major: "B"}, "source-b")

	if s.Current().Major != "B" {
		t.Errorf("after push B, current = %q, want %q", s.Current().Major, "B")
	}

	s.PopMode()
	if s.Current().Major != "A" {
		t.Errorf("after pop, current = %q, want %q", s.Current().Major, "A")
	}

	s.PopMode()
	if s.Current().Major != "base" {
		t.Errorf("after second pop, current = %q, want %q", s.Current().Major, "base")
	}
}

func TestSessionState_PopMode_EmptyStack(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})

	// Pop on empty stack returns current (no panic)
	result := s.PopMode()
	if result.Major != internal.MajorCoder {
		t.Errorf("PopMode on empty stack returned Major = %q, want %q", result.Major, internal.MajorCoder)
	}

	// Current unchanged
	got := s.Current()
	if got.Major != internal.MajorCoder {
		t.Errorf("Current().Major = %q, want %q", got.Major, internal.MajorCoder)
	}
}

func TestSessionState_PreviousMode(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})

	// Before any push, PreviousMode returns nil
	if s.PreviousMode() != nil {
		t.Errorf("PreviousMode before push should be nil")
	}

	// After push, PreviousMode returns the saved mode
	s.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "test")
	prev := s.PreviousMode()
	if prev == nil {
		t.Fatal("PreviousMode after push should not be nil")
	}
	if prev.Major != internal.MajorCoder {
		t.Errorf("PreviousMode().Major = %q, want %q", prev.Major, internal.MajorCoder)
	}

	// After pop, PreviousMode returns nil again (stack emptied)
	s.PopMode()
	if s.PreviousMode() != nil {
		t.Errorf("PreviousMode after pop should be nil")
	}
}

func TestSessionState_Source(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})

	// Default is empty
	if s.Source() != "" {
		t.Errorf("Source before set = %q, want %q", s.Source(), "")
	}

	// PushMode sets mode source
	s.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "skill: planner")
	if s.Source() != "skill: planner" {
		t.Errorf("Source = %q, want %q", s.Source(), "skill: planner")
	}

	// SetMode clears mode source
	s.SetMode(internal.ModeState{Major: internal.MajorCoder})
	if s.Source() != "" {
		t.Errorf("Source after SetMode = %q, want empty", s.Source())
	}
}

func TestSessionState_SkillSource(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})

	// Default is empty
	if s.SkillSource() != "" {
		t.Errorf("SkillSource before set = %q, want empty", s.SkillSource())
	}

	// Set via exported setter
	s.SetSkillSource("test-gen")
	if s.SkillSource() != "test-gen" {
		t.Errorf("SkillSource = %q, want %q", s.SkillSource(), "test-gen")
	}
}

func TestSessionState_SetSource(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	s.SetSource("custom-source")
	if s.Source() != "custom-source" {
		t.Errorf("Source = %q, want %q", s.Source(), "custom-source")
	}
}

func TestSessionState_ConcurrentReadsWrites(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})

	var wg sync.WaitGroup
	n := 100

	// Concurrent reads
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Current()
			_ = s.PreviousMode()
			_ = s.Source()
			_ = s.SkillSource()
		}()
	}

	// Concurrent writes
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SetMode(internal.ModeState{Major: internal.MajorPlanner})
			_ = s.PushMode(internal.ModeState{Major: internal.MajorReviewer}, "concurrent")
			_ = s.PopMode()
			s.SetSource("test")
			s.SetSkillSource("test-skill")
		}()
	}

	wg.Wait()
	// No race should occur (verified by -race flag)
}

func TestSessionState_PushMode_ClearsSourceOnPop(t *testing.T) {
	s := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})

	s.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "skill: test")

	// Mode source is set during push
	if s.Source() != "skill: test" {
		t.Errorf("Source = %q, want %q", s.Source(), "skill: test")
	}

	// Pop clears the mode source
	s.PopMode()
	if s.Source() != "" {
		t.Errorf("Source after pop = %q, want empty", s.Source())
	}
}
