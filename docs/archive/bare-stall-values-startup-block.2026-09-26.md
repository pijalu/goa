# Bare stall-timing values block startup ("60"/"45" without units) and the error doesn't show the required format

Archived: 2026-09-26 (fixed, tested, committed)

## Observed
After using /config and changing the stall settings, the home config ended up
with `execution.activity_timeout: "60"` / `activity_warn_after: "45"` (no
units) and the next launch died:

    Config error: validation errors (2):
    execution.activity_timeout: cannot parse "60" as duration: time: missing unit in duration "60"

(+ same for warn). The error message never said what format IS valid, and the
/config UI itself speaks plain seconds ("60"), so the file format's unit
requirement was invisible.

## Expected
(a) goa never PERSISTS a stall value the loader would reject — the written
    file always carries canonical durations ("60s") (write guard per the
    archived 2026-09-26 entry);
(b) duration validation errors show the required format explicitly (units
    required, e.g. use "60s");
(c) a bare-integer stall value in a config file is autocorrected at load
    (60 → 60s, matching the plain-seconds UI) with a visible warning naming
    the file, instead of blocking startup;
(d) self-heal over hard failure: when a config layer still fails validation
    after in-place heals, goa must NOT exit — it starts with the default
    config (defaults + surviving layers), tells the user exactly which
    file/values are wrong, and offers to CONFIRM the repair; on confirmation
    the corrected file is written (original backed up). Goa always aims to
    start, self-healing with user guidance.

## Fix plan (executed)
1. t1 — config package: `durationShapeHint`/`durationUnitsHint` in
   `config_validate.go` (error messages name the unit requirement and echo
   `did you mean "60s"?` for bare ints); `sanitizeBareStallDurations` in
   `loader_yaml.go` heals bare ints per layer at load with a stderr warning
   naming the file.
2. t2 — writer guard: `validateConfigBytes` runs `durationShapeError` so a
   write that would persist a bad duration shape is refused; unrelated field
   writes heal bare stall nodes via `healBareStallNodes` (file never wedges);
   `/config:set` persists the canonical committed value via
   `persistedValueForKey`/`canonicalStallValue` ("60" → writes "60s").
3. t3 — self-heal core: `LoadWithReport` (per-layer DROPPED problems, Healed
   list, defaults `FallbackErr`/`UsedDefaults`; `Load` semantics unchanged
   for callers that want the hard error only when defaults are broken);
   `RepairLayerFile` rewrites a broken layer file into a loadable shape
   (bare → "Ns", garbage key removed, contradictory warn dropped,
   unparseable YAML → empty mapping) with a timestamped `.bak-*` backup and
   a guarded, validated write.
4. t4 — self-heal UX: bootstrap `LoadConfig` uses `LoadWithReport` (fatal
   exit only when even the defaults cascade fails) and warns on stderr for
   non-TUI modes; the TUI app announces heals/drops/fallback as flashes and
   offers the confirmed repair through the clarify selector (Yes →
   `RepairLayerFile` per affected file + restart note; No/Esc → files left
   untouched); the config watcher keeps the last-good config when a hot
   reload reports drops/fallback instead of silently hot-swapping the
   running session to defaults.

## Validation
- New/updated tests: `config/bare_stall_duration_test.go`,
  `config/loader_report_test.go`, `config/write_guard_test.go`,
  `config/watcher_test.go`, `config/loader_modelref_heal_test.go`,
  `config/stall_timing_test.go`,
  `core/commands/config_stall_timing_test.go`
  (TestConfigSet_PersistsCanonicalSeconds asserts the raw persisted bytes
  carry `60s`/`45s` — the original poisoning is regression-pinned),
  `internal/app/config_repair_test.go` (clarify-driven Yes/No/silent paths
  against real files).
- Gates run separately, all clean:
  `go vet ./...` (0 issues) · `staticcheck ./...` (0 issues) ·
  `gocognit -over 15 .` (clean after splitting test helpers) ·
  `gocyclo -over 12 .` (clean after extracting
  `applyConfirmedRepair`/canonical-persist checkers) ·
  `go test -count=1 -race -cover ./...` → 0 FAIL, 87 packages ok.
