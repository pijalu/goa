# bugs-20260823-statusbar-stale-provider — Wrong statusbar after selecting ox-alpha and sending a message

**Reported:** 2026-08-23 · **Fixed:** 2026-08-23 · **Commit:** fix(model): carry picker provider through custom-model selection

## Symptom

Selecting `ox-alpha` (served by configured provider `stealth`, API name
`stealth/ox-alpha`) from `/model`'s "Select model:" picker while
`openai-codex` was active left the status bar showing the mixed pair

    (openai-codex) stealth/ox-alpha

and — worse — subsequent requests went to the openai-codex endpoint asking
for a stealth model.

## Root cause

The "Select model:" picker built by `promptCustomModel` aggregates models
from ALL configured providers (`fetchAllProviderModels`), so every entry
already knows its `ProviderID`. But `customModelSelectionHandler` threw
that information away and called `applyModelSelection(selected)` with only
the model ID. `applyModelSelection` re-derived the provider with
`providerIDForModel`, which only knows locally *configured* models
(`cfg.Models`); a remote/catalog ID like `stealth/ox-alpha` is not there,
so the lookup returned "" — meaning "keep current provider". The coupled
switch then legitimately produced provider=openai-codex +
model=stealth/ox-alpha, which the footer faithfully rendered as the mixed
pair. The switch ordering itself (manager → config → persist → agent) was
correct; the input to it was wrong.

## Fix plan (as planned before executing)

1. Add `applyModelSelectionForProvider(host, cfg, saver, pickerProvider,
   selected)`: an explicit provider candidate from the selection context
   wins over the `cfg.Models` re-derivation; empty keeps legacy behavior.
2. `applyModelSelection` becomes a thin wrapper passing "" (free-text
   custom models keep the documented "stay on current provider" behavior).
3. `customModelSelectionHandler` gains a `providerByID` map parameter;
   `promptCustomModel` builds it from the fetch list via
   `providerByModelID` (first-entry-wins, mirroring dedup order).

Test approach: regression tests driving `applyModelSelectionForProvider`
and the handler wiring with a recording provider manager + fake agent
manager: assert manager/config/persisted surfaces all carry
(stealth, stealth/ox-alpha); guard the free-text path still keeps the
current provider; cover the map's first-entry-wins rule.

Validation steps: run new tests + full core/commands suite (+race);
quality gates separately; commit; archive.

## Validation

- New tests in `core/commands/model_picker_provider_test.go`:
  CarriesPickerProvider, UsesProviderMap, CustomKeepsCurrentProvider,
  ConfiguredModelStillSwitches, ProviderByModelID_FirstEntryWins — pass.
- `go test ./core/commands -count=1` and `-race` — pass.
- Gates: go vet clean; staticcheck one pre-existing SA1019 in unrelated
  model_test.go (noted per guideline); gocognit/gocyclo match the
  pre-existing baseline in unmodified files.

## Notes

- The statusbar formatting (`modelDisplay`) was verified correct: given a
  consistent couple it renders `(stealth) …` or the vendor-prefixed bare
  name — never a stale prefix. No footer change needed.
