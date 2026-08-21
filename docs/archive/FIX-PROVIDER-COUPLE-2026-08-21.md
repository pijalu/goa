# FIX — Provider not always updated: atomic provider/model couple

> **Closed**: 2026-08-21 · commit `505b23e` (feature/multi-agent)
> **Source**: bugs.md report "Provider not always updated"

## Report
Provider can sometimes be incorrect in the status bar, usually after changing
a model: e.g. model is openrouter but shows as openai-codex:
`(openai-codex) stealth/ox-alpha`.
Make sure the provider/couple are *always* updated together.

## Root causes (all four fixed)
1. `/provider <id>` (`switchProvider`) and the picker
   (`applyProviderSelection`) mutated config + persisted but NEVER called
   `propagateModelSwitch` → live agent session kept previous provider's
   model/stream options (next turn on stale credentials).
2. `/model` paths mutated+persisted cfg BEFORE `ProviderManager.SetActive`;
   a rejected SetActive left cfg=X while manager/agents=Y → mixed couple.
3. `config set active_model` propagated only when the provider CHANGED;
   same-provider switches never reached the live agent.
4. `core/dream.go resolveModel` permanently overwrote ActiveProvider/
   ActiveModel with the dream couple, never restored.

## Fix plan (executed)
- New `applyCoupledSwitch(host, cfg, saver, providerID, modelID)` in
  core/commands/model.go — single atomic choke point. Order guarantees
  "always updated together": manager SetActive FIRST (rejection ⇒ abort with
  nothing mutated anywhere), then config commit, persist via saver,
  propagateModelSwitch (agent SetModel + rebuilt stream options + per-model
  thinking level), FooterRefresh.
- runModelCommand / applyModelSelection / switchProvider /
  applyProviderSelection rewired through it.
- config_cli: propagate on every active_model switch; provider persistence
  extracted to persistActiveModelProviderSwitch.
- dream.go: defer-restore of the previous couple around resolveModel.

## Test approach & validation results
- core/commands/couple_test.go: TestSwitchProvider_PropagatesCoupleToAgent,
  TestApplyProviderSelection_PropagatesCoupleToAgent (agent session follows
  switch; recording PM sees the couple), TestModelCommand_ManagerRejectKeepsCouple
  (rejected SetActive leaves couple untouched + inline message),
  TestApplyCoupledSwitch_KeepCurrentProvider ("" keeps current provider). PASS
- core/dream_couple_test.go: TestDreamResolveModel_RestoresActiveCouple
  (dream override restored; no-override case undisturbed). PASS
- Gates (each separately): go vet ./... OK · staticcheck ./... OK ·
  gocognit -over 15 . only pre-existing warnings in untouched files
  (noted per guideline exception) · gocyclo -over 12 . clean for changed files ·
  go test -count=1 -race -cover ./... all green.
