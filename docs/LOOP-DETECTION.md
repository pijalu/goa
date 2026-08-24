# Loop detection

Goa has several independent safeguards for runaway model behaviour. They do not share a single counter, so changing one setting does not change the others. Values below are configured under `execution` in YAML (or through `/config` where noted). A value of `0` means the documented default unless stated otherwise.

## Tool-call repeat detector

This detector watches identical tool calls (same tool and arguments) and reports a loop when they repeat consecutively.

| Setting | Default | Purpose |
|---|---:|---|
| `loop_warning` | 10 | Same tool-call streak that produces a warning. |
| `loop_interrupt` | 15 | Same tool-call streak that interrupts execution. Must exceed `loop_warning`. |
| `max_tool_repeat_total` | off | Maximum identical calls in a turn, regardless of spacing. |
| `max_tool_repeat_consecutive` | 2 | Maximum consecutive identical calls in a turn. |
| `max_tool_calls` | 200 | Maximum occurrences of one identical call in the rolling window. This is **not** a total tool-call allowance. |
| `tool_call_limit_reset_window` | 10 | Number of recent calls considered by the rolling-window guard. |
| `disable_tool_budget` | false | Disables the agent tool-call budget guards. It does not disable the core loop detector. |

The interactive path is `/config` → **Loop detection** → **Thresholds**. The same values can be set with `/config:set:execution.<key>:<value>`.

## Consecutive tool-call rounds

`max_consecutive_tool_rounds` (default **15**) limits consecutive LLM rounds that end in tool calls. It catches loops where every call is different, so duplicate-call guards never fire. When reached, Goa injects an internal recovery instruction asking the model to answer with the information already gathered. This is the source of the “per-turn tool-call round limit” message.

It is available in `/config` → **Loop detection** → **Thresholds**. Set it to `0` to disable it. This is separate from `max_tool_calls`: the embedded default is **200**, and changing that duplicate-call limit does not raise this round limit.

## Thinking/reasoning detector

The thinking detector tracks repeated significant reasoning lines and low-diversity reasoning windows within one turn.

| Setting | Purpose |
|---|---|
| `disable_thinking_loop_detection` | Persistently disable thinking-loop detection. |
| `disable_thinking_stall_detection` | Persistently disable the reasoning-only stall watchdog. |
| `thinking_stall_warn_seconds` | Delay before a reasoning-only stall warning. |
| `thinking_stall_stop_seconds` | Delay before stopping a reasoning-only stall. |

Thinking-loop thresholds use the runtime loop-detector defaults and are not currently separate YAML fields. Session-only overrides are available with `/config:temp:think_loop_detection:on|off` and `/config:temp:thinking_stall_detection:on|off`.

## Streaming text detector

This detector catches repeated text in the assistant's streamed output, even when no tool is called.

| Setting | Default | Purpose |
|---|---:|---|
| `stream_loop_max_repeats` | 5 | Repeated blocks required before stopping the stream. |
| `stream_loop_min_period` | 50 | Minimum repeated unit length in characters. Values below 8 are rejected. |
| `stream_loop_max_strikes` | 3 | Loop detections before stopping the turn. |
| `stream_loop_reset_after` | 10 | Clean messages/tool calls before resetting strikes. |
| `disable_stream_loop_detection` | false | Persistently disable this detector. |

Session-only override: `/config:temp:stream_loop_detection:on|off`.

## Configuration examples

```yaml
execution:
  loop_warning: 10
  loop_interrupt: 15
  max_tool_repeat_total: 15
  max_tool_repeat_consecutive: 2
  max_tool_calls: 200
  tool_call_limit_reset_window: 10
  max_consecutive_tool_rounds: 15
  stream_loop_max_repeats: 5
  runaway_loop_max_repeats: 2
```

If a warning appears sooner than expected, identify which detector produced it. In particular, `max_tool_calls` controls duplicate calls in a rolling window; it does not control consecutive tool-call rounds.
