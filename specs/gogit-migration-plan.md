# go-git Migration & Agent Git Tool — Research & Plan

Date: 2026-07-30 · Status: **DROPPED 2026-07-30** — verdict delivered, not pursued;
retained as a decision record (revive Phases 1–2 only if a concrete need returns) · Author: agent research session

## Objective

Evaluate removing Goa's dependency on executing the `git` binary — via
[go-git](https://github.com/go-git/go-git) (local source: `/Users/muaddib/dev/go-git`) —
and offering agents a dedicated `git` tool instead of running git through `bash`.
**Constraint set by the requester: only do what carries an actual advantage; be
critical of pros/cons.**

## Verdict (critical summary)

**Total removal of git execution: INVALIDATED.** go-git has hard gaps that hit
Goa's core flows: no `git apply` (orchestrator worktree sync), no hook execution
on commit (pre-commit/husky silently bypassed = correctness bug), no stash /
rebase / non-FF merge / LFS / gc / reflog, linked worktrees only in v6's
experimental `x/` package, and `Status()` is materially slower than C git on
large repos (footer hot path).

**Surgical replacement + a dedicated agent git tool: VALIDATED.**
1. Plugin install clone → go-git removes a *user-facing* git-binary dependency
   (Windows) and the user-URL shell-out class entirely. Clear win, low risk.
2. An agent `git` tool is justified by guardrails + structured output, not by
   go-git: today `bash` offers zero friction for `git push --force`,
   `reset --hard`, etc. (the bash jail only checks paths). The tool should be
   backend-pluggable (`exec` default for full fidelity incl. hooks/signing,
   `gogit` where equivalent or where no git binary exists).

## Current git-execution inventory (post-BackupStager, 2026-07-30)

| # | Site | Operations | Notes |
|---|------|-----------|-------|
| 1 | `plugins/manager.go` | `git clone --depth 1` | plugin install; injectable `cloneFunc` already |
| 2 | `internal/gitworktree.go` | `worktree add/remove`, `diff HEAD \| git apply` | orchestrator isolation |
| 3 | `internal/review/git.go` | `rev-parse`, `status --porcelain`, `diff`, `log`, `--show-toplevel` | `/review` command |
| 4 | `tui/footer.go` | `rev-parse --abbrev-ref`, `status --porcelain` | event-driven footer refresh (hot path) |
| 5 | `core/commands/execution.go` | `diff --name-only [--cached]`, `checkout --`, `restore --staged` | `/undo` (cmd.Dir fixed 2026-07-30) |
| 6 | agent via `bash` tool | **any** git command | no semantic guardrails today |

(Former `tools/common/gitutil.go` `git add`/`ls-files` site eliminated by the
BackupStager change — recovery points are now file-based.)

## go-git capability evidence

Verified against local source `v6.0.0-alpha.5` (main) and tag `v5.19.2`
(latest stable); feature matrix from `COMPATIBILITY.md` + code inspection.

**Works**: clone (incl. `--depth`, auth: token/user+pass/ssh/ssh-agent),
init, add, status, commit, reset, branch, checkout (basic), tag, fetch, pull
(FF only), push, log, show, grep, blame, diff (tree-to-tree `Patch` with
unified output), `insteadOf` URL rewrites (`config/config.go:659`), SCP-like
`git@host:path` URLs (`plumbing/transport/url.go`), sparse-checkout, submodules
(mostly), repack.

**Gaps that matter to Goa**:
- `apply` ❌ — kills a pure go-git `WorktreeManager.Apply` (site 2). Writing
  our own patch applier is out of scope.
- Linked worktrees — only v6 `x/plumbing/worktree` (experimental, alpha); v5
  has nothing. Orchestrator isolation is core: do not bet on `x/`.
- Commit hooks ❌ — go-git never runs `.git/hooks/*` or `core.hooksPath`.
  A commit tool built on go-git silently bypasses repo policy.
- Commit signing — PGP `Signer` ✓, but external `gpg.program` / SSH signing ❌.
- `stash`, `rebase`, non-FF `merge`, `describe`, `reflog`, `gc`, `lfs` ❌ —
  agents still need bash for these regardless: the tool can never be the
  only git path.
- Worktree(unstaged)-vs-HEAD unified diff: no one-call API (patches are built
  from tree-to-tree `Change`s; `getPatch` is unexported). Site 3's default
  case (`git diff HEAD` on a dirty tree) would need hand-rolled blob diffing
  via `plumbing/format/diff` — real code we would own.
- Credential fidelity: no `credential.helper` support, no `~/.ssh/config`
  evaluation (ssh-agent ✓). Private plugin repos that clone fine via the
  user's git CLI can fail under go-git → **exec fallback required on auth
  failure**.
- `Status()` walks the worktree (ignore-aware, pruned) — fine occasionally,
  risky on the footer's refresh cadence for big repos vs C `status --porcelain`.

## Per-site disposition

| Site | Verdict | Rationale |
|------|---------|-----------|
| 1 plugin clone | **Migrate (Phase 1)** | removes git-binary requirement for a user-facing feature (Windows); kills user-URL shell-out class; shallow plugin repos = small/safe; auth-failure → exec fallback |
| 2 worktree mgr | **Keep exec** | `apply` unsupported; linked worktrees alpha-only |
| 3 review git | **Keep exec** (optional gogit for committed-range diff later) | default case is dirty-worktree diff = hand-rolled in go-git; not perf-critical but fidelity matters for review |
| 4 footer | **Keep exec** | hot path; degrades gracefully without git binary |
| 5 /undo | **Keep exec** | trivial commands; user-facing; works |
| 6 agent bash-git | **Add git tool (Phases 2–3)** | guardrails + structured output + rendering; backend-pluggable |

## Plan

### Phase 1 — plugin clone via go-git (validated, do first)
- Add `github.com/go-git/go-git/v5 v5.19.2` (stable; v6 is alpha and only
  needed for `x/` worktrees we deliberately avoid).
- `plugins/gogit_clone.go`: `cloneGoGit(url, dir)` → `git.PlainClone(dir,
  false, &git.CloneOptions{URL: url, Depth: 1, SingleBranch: true, Auth:
  defaultAuth(url)})`; `defaultAuth` tries ssh-agent for ssh URLs, nil
  otherwise (https anonymous).
- `Manager.Install`: try go-git clone; on transport/auth error **fall back to
  exec clone** (preserves credential-helper/`~/.ssh/config` behavior); keep
  `SetCloneFunc` test injection.
- Tests: clone from a local bare repo via go-git (no exec); fallback path
  triggers on auth failure; `validateSource` unchanged (still rejects http://).
- Rollback: one-line switch back to exec in `runGitClone` default.

### Phase 2 — agent `git` tool (exec backend, guardrails-first)
- `tools/git.go` + `tools/git_renderer.go` + `git.long.md`/`git.short.md`
  (embedded docs convention), registered in `registerTools` +
  `tui/register_renderers.go`; opt-out config flag like `verify`.
- Schema: `{op: status|diff|log|add|commit|branch|checkout, args...}` —
  vetted subcommand allowlist; **reject** destructive flags (`push --force`,
  `reset --hard`, `clean -f`, `branch -D`, `checkout -- .`) unless an explicit
  `confirm_destructive` param; scope all paths to the repo root.
- Backend interface `gitops.Backend` (narrow ops, SOLID); **exec backend
  first** — full fidelity: hooks, signing, every porcelain nuance.
- Structured output (typed status entries, diff stat + patch, log records) →
  better model consumption + chroma-highlighted diff rendering in TUI.
- Tests: per op incl. guardrail rejections, non-repo error, detached HEAD.
- Prompt/docs steer the model to prefer the git tool over bash-git; bash
  remains for the long tail (stash/rebase/merge/LFS) by design.

### Phase 3 — gogit backend for the tool
- Read ops (status/diff committed ranges/log) via go-git — equivalent
  fidelity, no binary needed.
- `commit` via go-git only when **no hooks configured** (check
  `core.hooksPath` + `.git/hooks/*` executables) and **no signing configured**
  (`commit.gpgsign`); otherwise delegate to exec backend — hybrid inside the
  tool. Alternative: execute hooks ourselves via existing exec infra (more
  code; defer).
- Enables end-to-end git-less operation: plugin install + repo init/clone/
  commit on machines without git.
- Benchmark gate: `Status()` parity check on a large repo before defaulting
  read ops to gogit.

### Phase 4 — optional call-site cleanups (only if benchmarks say parity)
- `internal/review`: committed-range diffs (`base..HEAD`) via
  `object.DiffTree`+`Patch`; keep exec for dirty-worktree diff.
- Nothing else migrates. Explicit non-goals: footer status, worktree
  add/apply, stash/rebase/merge/LFS ops in the tool.

## Risks & mitigations
- **Auth regressions on private plugin repos** → exec fallback in Phase 1;
  test both paths.
- **Two git paths confuse the model** → crisp tool docs; allowlist errors
  explicitly say "use bash for stash/rebase/…".
- **go-git dependency weight/maintenance** → pin v5 stable; isolate behind
  `gitops.Backend` so removal is cheap.
- **Silent hook bypass on commit** → Phase 3 delegation rule is mandatory,
  not optional; test with a failing pre-commit hook.
- **Perf regression on status** → benchmark gate before gogit read ops become
  default; exec stays default in Phase 2.

## Appendix — evidence commands
- Feature matrix: `COMPATIBILITY.md` @ v6.0.0-alpha.5 (`apply` ❌, `stash` ❌,
  `rebase` ❌, merge FF-only, `lfs` ❌, `gc` ❌, `reflog` ❌; `diff` ✅ unified).
- Depth/auth/SCP/insteadOf: `options.go`, `plumbing/transport/url.go:14`,
  `config/config.go:659`, `plumbing/transport/ssh/sshagent/`.
- Linked worktrees: `x/plumbing/worktree/worktree.go` + `_examples/worktrees`
  (v6-only, experimental); absent in v5 (`git ls-tree v5.19.2` = 0 hits).
- Hooks: no hook execution in commit path (only `preReceiveHook` in
  `worktree_wasi.go`, unrelated).
- Worktree diff gap: patches built from `Change` (`plumbing/object/patch.go`,
  unexported `getPatch`); no `Worktree.Diff` API.
