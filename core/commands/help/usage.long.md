# /usage

Show cumulative LLM token usage across all your Goa sessions, like `opencode-stats`.

Usage is recorded to a global SQLite database (`~/.goa/usage.db`) each time a model turn completes, tagged with the project directory, provider, and model.

## Synopsis

```
/usage[:scope]
```

## Scopes

| Scope | Description |
| --- | --- |
| `/usage` or `/usage:all` | Global totals plus per-project, per-provider, and per-model breakdowns |
| `/usage:project` | Usage grouped by project |
| `/usage:provider` | Usage grouped by provider |
| `/usage:model` | Usage grouped by model |
| `/usage:here` | Usage for the current project only (provider + model breakdown) |

## Examples

```
/usage
/usage:model
/usage:here
```
