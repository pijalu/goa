// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"sync"

	"github.com/pijalu/goa/tui"
)

// WizardResult carries the configured provider + profile back to main.go.
type WizardResult struct {
	ProviderAdded bool
	ConfigWritten bool
	Cancelled     bool
}

// RunSetupWizard launches the interactive setup wizard using the TUI engine.
func RunSetupWizard(projectDir string, loader *CascadeLoader) (*WizardResult, error) {
	cfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load config for wizard: %w", err)
	}

	term := tui.NewProcessTerminal()
	engine := tui.NewTUI(term)

	done := make(chan *WizardResult, 1)
	w := newWizardComponent(cfg, loader, projectDir, done)
	w.tui = engine
	engine.AddChild(w)
	engine.SetFocus(w)
	w.initDefaults()

	if err := engine.Start(); err != nil {
		return nil, err
	}

	tuiStopped := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		select {
		case <-engine.Stopped():
		case <-tuiStopped:
		}
		stopOnce.Do(func() { close(tuiStopped) })
	}()

	var result *WizardResult
	select {
	case result = <-done:
	case <-tuiStopped:
	}

	engine.Stop()
	stopOnce.Do(func() { close(tuiStopped) })

	if result == nil {
		result = &WizardResult{Cancelled: true}
	}
	return result, nil
}

// -- Wizard state machine -----------------------------------------

type wizardState int

const (
	stateWelcome wizardState = iota
	stateProviderType
	stateProviderEndpoint
	stateProviderKey
	stateProviderTest
	stateModelSelect
	stateModelSetup
	stateModelAdvanced
	stateWebFetchSummary
	stateCompanionModel
	stateDreamModel
	stateCompanionModelSetup
	stateCompanionProviderType
	stateCompanionProviderEndpoint
	stateCompanionProviderKey
	stateCompanionProviderTest
	stateCompanionModelSelect
	stateCompanionModelAdvanced
	stateMode
	stateSkillMode
	stateAdvancedOptions
	statePromptPreview
	stateWorkflowPreview
	stateDone
)

// modelSlotProviderModelFieldIndex defines fields in the model setup form.
const (
	modelFieldModelID = iota
	modelFieldModelName
	modelFieldModelTemp
	modelFieldModelMaxTokens
	modelFieldCount // total number of model fields
)

// modelSlot holds the provider + model configuration for one slot (main or companion).
type modelSlot struct {
	providerID          string
	providerName        string
	selectedPresetIndex int
	endpoint            string
	apiKey              string
	modelID             string
	modelName           string
	modelTemp           string
	modelMaxTokens      string
	modelFieldIdx       int
	modelReasoning      bool
	modelThinkingLevel  string
	availableModels     []string
	selectedModelIdx    int
}

type wizardComponent struct {
	config     *Config
	loader     *CascadeLoader
	projectDir string
	tui        *tui.TUI
	done       chan<- *WizardResult
	state      wizardState
	cancelled  bool
	saved      bool
	saveErr    error

	main      modelSlot
	companion modelSlot

	selectedMode int
	inputMode    string
	editor       *tui.LineEditor

	companionModelSelected bool
	copyPrompts            bool
	copyWorkflows          bool
	previewYesNo           int
	focused                bool

	skillMode              int // 0 = sub-agent, 1 = inline
	advancedMode           int // 0 = defaults, 1 = customize
	maxToolRepeat          string
	compressEnabled        int // 0 = no, 1 = yes
	compressMaxTokens      string
	compressThreshold      string
	allowFuzzEdits         int // 0 = no, 1 = yes
	advancedFieldIdx       int
	webfetchSummaryEnabled int // 0 = no, 1 = yes

	// Dream mode settings
	dreamEnabled          int // 0 = no, 1 = yes
	dreamAuto             int // 0 = no, 1 = yes
	dreamApplyAfterReview int // 0 = no, 1 = yes
}

func newWizardComponent(cfg *Config, loader *CascadeLoader, projectDir string, done chan<- *WizardResult) *wizardComponent {
	w := &wizardComponent{
		config:     cfg,
		loader:     loader,
		projectDir: projectDir,
		done:       done,
		state:      stateWelcome,
		editor:     tui.NewLineEditor(),
	}
	w.focusProvider(0)
	w.initDefaults()
	return w
}

func (w *wizardComponent) initDefaults() {
	w.main.modelTemp = "0.2"
	w.main.modelMaxTokens = "0"
	w.main.modelReasoning = true
	w.main.modelThinkingLevel = "high"
	w.companion.modelTemp = "0.2"
	w.companion.modelMaxTokens = "0"
	w.companion.modelReasoning = true
	w.companion.modelThinkingLevel = "high"
	w.maxToolRepeat = "1"
	w.compressEnabled = 0
	w.compressMaxTokens = "0"
	w.compressThreshold = "100"
	w.allowFuzzEdits = 1 // default: on
	w.dreamEnabled = 1
	w.dreamAuto = 0
	w.dreamApplyAfterReview = 0
}

func (w *wizardComponent) finish() {
	select {
	case w.done <- &WizardResult{ProviderAdded: w.main.providerID != "", ConfigWritten: w.saved, Cancelled: w.cancelled}:
	default:
	}
}

// -- Component interface ------------------------------------------
