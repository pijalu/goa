#!/usr/bin/env bash
# w3_attach_multiproject.sh — drive `goa attach` and the multi-project
# supervisor in the REAL binary and assert the DOCUMENTED CONTRACT as
# expected results. This is the suite that keeps docs and code honest: every
# assertion here is a claim made by README.md / docs/WEBUI.md / `--help`
# output, checked against actual process behavior.
#
#   help_flags        `goa server --help` documents every server flag the
#                     docs describe (projects-root, session-idle,
#                     max-sessions, auth, read-only, cells)
#   attach_help       `goa attach --help` documents its flags (server,
#                     session, path, plane, auth)
#   help_topics       `goa help server` and `goa help attach` answer
#   sup_health        supervisor /healthz answers with sessions:0, session:""
#   connect_opens     POST /connect {path} spawns a session and returns its id
#   connect_reuses    a second connect to the same path returns the SAME id
#                     and does not spawn a second child
#   connect_badpath   a relative path is refused (400) with the documented
#                     reason before any child spawns
#   connect_escape    a symlink pointing outside the root is refused
#   sessions_list     GET /sessions lists the live session
#   max_sessions      --server-max-sessions 1 refuses the second DISTINCT
#                     project with the documented limit error
#   form_redirect     the browser form (urlencoded /connect) redirects to
#                     /s/<id>; a rejected path gets an HTML error page
#   proxied_text      /s/<id>/text serves the child's live grid
#   attach_flow       attach --path under a PTY renders the TUI, typed keys
#                     reach the server grid, Ctrl+] detaches, session lives
#   plain_fallback    a plain server has no /connect (404); /healthz names
#                     the session; attach falls back and attaches
#   idle_reap         --server-session-idle 2s reaps an untouched session at
#                     the reaper tick; the transcript stays on disk; a new
#                     connect re-spawns
#   shutdown_clean    stopping the supervisor leaves no children or listeners
#
# Requires: curl, expect (PTY for the attach flow), python3.
# Usage:
#   E2E_ROOT=/tmp/w3 GOA_BIN=/tmp/goa-e2e/goa e2e/w3_attach_multiproject.sh

set -euo pipefail
cd "$(dirname "$0")/.."
source e2e/lib.sh
export GOA_BIN   # the expect helper reads $env(GOA_BIN)

: "${E2E_ROOT:=/tmp/goa-w3}"
E2E_ROOT="$E2E_ROOT/w3-$(date +%H%M%S)"
mkdir -p "$E2E_ROOT"
: > "$E2E_ROOT/results.tsv"

PORT="${GOA_WEB_PORT:-18811}"

expect_has() { # <what> <haystack> <needle...> — records and continues
  local what="$1" hay="$2"; shift 2
  local n missing=""
  for n in "$@"; do
    grep -q -- "$n" <<<"$hay" || missing="$missing $n"
  done
  if [ -n "$missing" ]; then
    record "$what" FAIL "missing:$missing"
    fail "$what: missing$missing"
    return 0
  fi
  record "$what" PASS "$*"
  pass "$what: all documented flags present"
}

# ---------------------------------------------------------------- build
log "building goa"
mkdir -p "$(dirname "$GOA_BIN")"
( go build -o "$GOA_BIN" ./cmd/goa/ )

# ---------------------------------------------------------- help flags
log "documented contract: goa server --help / goa attach --help / help topics"
SRV_HELP="$("$GOA_BIN" server --help 2>&1 || true)"
expect_has help_flags "$SRV_HELP" \
  "--server-projects-root" "--server-session-idle" "--server-max-sessions" \
  "--server-addr" "--server-read-only" "--server-max-clients" "--server-cells" \
  "--server-auth" "--server-auth-token" "--insecure-no-auth"

ATT_HELP="$("$GOA_BIN" attach --help 2>&1 || true)"
expect_has attach_help "$ATT_HELP" \
  "--server" "--session" "--path" "--plane" \
  "--server-auth-token" "--server-auth-user" "--server-auth-password"

HELP_SERVER="$( "$GOA_BIN" help server 2>&1 || true )"
HELP_ATTACH="$( "$GOA_BIN" help attach 2>&1 || true )"
if grep -q "multi-project" <<<"$HELP_SERVER" \
   && grep -q "drive a goa server session from this terminal" <<<"$HELP_ATTACH" \
   && grep -q -- "--server-max-sessions" <<<"$HELP_SERVER" \
   && grep -q -- "--path DIR" <<<"$HELP_ATTACH"; then
  record help_topics PASS "goa help server + goa help attach"
  pass "help_topics: both topics answer with their documented content"
else
  record help_topics FAIL "server=[$(head -c 120 <<<"$HELP_SERVER")] attach=[$(head -c 120 <<<"$HELP_ATTACH")]"
  fail "help_topics"
fi

# ------------------------------------------------- environment: projects
ROOT="$E2E_ROOT/root"
PROJ_A="$ROOT/proj-a"
PROJ_B="$ROOT/proj-b"
mkdir -p "$PROJ_A" "$PROJ_B"
ln -sfn "$PROJ_B" "$ROOT/inside-link"     # symlink to a dir INSIDE the root (allowed)
ln -sfn /private/etc "$ROOT/escape-out"   # symlink pointing OUTSIDE (refused)

# start_supervisor claims the port for itself: any previous occupant (an
# orphaned run) is killed first, and readiness is verified against OUR pid —
# a health answer from a stale process would poison every later assertion.
start_supervisor() { # <port> <extra args...>
  local port="$1"; shift
  lsof -ti ":$port" 2>/dev/null | xargs kill -9 2>/dev/null || true
  sleep 0.3
  # `exec` makes the recorded pid the goa process itself — killing the
  # pidfile's pid must kill the server, not a wrapper shell.
  ( cd "$E2E_ROOT" && exec "$GOA_BIN" server --server-addr "127.0.0.1:$port" "$@" \
      > "$E2E_ROOT/sup-$port.log" 2>&1 ) & echo $! > "$E2E_ROOT/sup-$port.pid"
  local i pid
  pid="$(cat "$E2E_ROOT/sup-$port.pid")"
  for i in $(seq 1 50); do
    if ! ps -p "$pid" >/dev/null 2>&1; then
      fail "supervisor on :$port exited at startup (see sup-$port.log)"
      return 1
    fi
    if curl -s -m 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  fail "supervisor on :$port did not come up"
  return 1
}

# cleanup runs on every exit path — pass or crash: a suite that leaks a
# supervisor poisons the next run through its port.
cleanup() {
  local pf pid
  for pf in "$E2E_ROOT"/sup-*.pid "$E2E_ROOT"/plain.pid; do
    [ -f "$pf" ] || continue
    pid="$(cat "$pf")"
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
  for pf in "$E2E_ROOT"/sup-*.pid "$E2E_ROOT"/plain.pid; do
    [ -f "$pf" ] || continue
    pid="$(cat "$pf")"
    ps -p "$pid" >/dev/null 2>&1 && kill -9 "$pid" 2>/dev/null || true
  done
  pkill -9 -f "goa server --server-addr unix://" 2>/dev/null || true
  return 0
}
trap cleanup EXIT

connect_json() { # <port> <path>
  local port="$1" path="$2"
  curl -s -m 130 -X POST -H 'Content-Type: application/json' \
    -d "{\"path\":\"$path\"}" "http://127.0.0.1:$port/connect"
}

stop_supervisor() { # <port>
  local pidfile="$E2E_ROOT/sup-$1.pid"
  [ -f "$pidfile" ] && kill "$(cat "$pidfile")" 2>/dev/null || true
  sleep 1
  if [ -f "$pidfile" ] && ps -p "$(cat "$pidfile")" >/dev/null 2>&1; then
    kill -9 "$(cat "$pidfile")" 2>/dev/null || true
  fi
}

# ------------------------------------------------ supervisor + connect
log "starting supervisor (no caps) on :$PORT"
start_supervisor "$PORT" --server-projects-root "$ROOT"

HZ="$(curl -s -m 5 "http://127.0.0.1:$PORT/healthz")"
if grep -q '"session":""' <<<"$HZ" && grep -q '"sessions":0' <<<"$HZ"; then
  record sup_health PASS "$HZ"
  pass "sup_health: empty session index at boot"
else
  record sup_health FAIL "$HZ"
  fail "sup_health: $HZ"
fi

R_A="$(connect_json "$PORT" "$PROJ_A")"
SESS_A="$(python3 -c 'import sys,json;print(json.load(sys.stdin).get("session",""))' <<<"$R_A")"
if [ -n "$SESS_A" ]; then
  record connect_opens PASS "session $SESS_A"
  pass "connect_opens: $SESS_A"
else
  record connect_opens FAIL "$R_A"
  fail "connect_opens: $R_A"
fi

R_A2="$(connect_json "$PORT" "$PROJ_A/")"   # trailing slash = same directory
SESS_A2="$(python3 -c 'import sys,json;print(json.load(sys.stdin).get("session",""))' <<<"$R_A2")"
if [ "$SESS_A2" = "$SESS_A" ]; then
  record connect_reuses PASS "same id on reconnect"
  pass "connect_reuses"
else
  record connect_reuses FAIL "first=$SESS_A again=$SESS_A2"
  fail "connect_reuses"
fi

BAD="$(connect_json "$PORT" "relative/path")"
if grep -q "absolute" <<<"$BAD"; then
  record connect_badpath PASS "$BAD"
  pass "connect_badpath refused with reason"
else
  record connect_badpath FAIL "$BAD"
  fail "connect_badpath: $BAD"
fi

ESC="$(connect_json "$PORT" "$ROOT/escape-out")"
if grep -q "outside the projects root" <<<"$ESC"; then
  record connect_escape PASS "$ESC"
  pass "connect_escape refused with reason"
else
  record connect_escape FAIL "$ESC"
  fail "connect_escape: $ESC"
fi

INSIDE="$(connect_json "$PORT" "$ROOT/inside-link")"
SESS_IN="$(python3 -c 'import sys,json;print(json.load(sys.stdin).get("session",""))' <<<"$INSIDE")"
R_B_REF="$(connect_json "$PORT" "$PROJ_B")"
SESS_B_REF="$(python3 -c 'import sys,json;print(json.load(sys.stdin).get("session",""))' <<<"$R_B_REF")"
if [ -n "$SESS_IN" ] && [ "$SESS_IN" = "$SESS_B_REF" ]; then
  record connect_symlink_inside PASS "inside symlink resolves to the same session"
  pass "connect_symlink_inside"
else
  record connect_symlink_inside FAIL "inside=$INSIDE direct=$R_B_REF"
  fail "connect_symlink_inside"
fi

SL="$(curl -s -m 5 "http://127.0.0.1:$PORT/sessions")"
if grep -q "$SESS_A" <<<"$SL" && grep -q "proj-a" <<<"$SL"; then
  record sessions_list PASS "$SL"
  pass "sessions_list"
else
  record sessions_list FAIL "$SL"
  fail "sessions_list: $SL"
fi

# ------------------------------------------------ browser form + proxied text
log "browser form + proxied session surface"
FORM="$(curl -s -m 130 -X POST -d "path=$PROJ_A" "http://127.0.0.1:$PORT/connect" \
  -o /dev/null -w '%{http_code} %{redirect_url}')"
if grep -q "^303 " <<<"$FORM" && grep -q "/s/$SESS_A" <<<"$FORM"; then
  record form_redirect PASS "$FORM"
  pass "form_redirect: 303 to the session page"
else
  record form_redirect FAIL "$FORM"
  fail "form_redirect: $FORM"
fi

TXT="$(curl -s -m 10 "http://127.0.0.1:$PORT/s/$SESS_A/text")"
if grep -q "▄\|goa coding agent" <<<"$TXT"; then
  record proxied_text PASS "child grid served through the proxy"
  pass "proxied_text"
else
  record proxied_text FAIL "no TUI content in the mirror"
  fail "proxied_text"
fi

# ------------------------------------------------ attach flow (real PTY)
log "attach --path under a PTY: render, type, detach, session survives"
SESS_FILE="$E2E_ROOT/sess.txt"
printf '%s' "$SESS_A" > "$SESS_FILE"
# The heredoc is quoted: expect receives literal Tcl and reads every value
# from its environment (GOA_BIN, W3_PORT, W3_PROJ are exported above).
export W3_PORT="$PORT" W3_PROJ="$PROJ_A"
cat > "$E2E_ROOT/attach.exp" <<'EXPECT'
#!/usr/bin/expect -f
set timeout 90
log_user 0
spawn $env(GOA_BIN) attach --server 127.0.0.1:$env(W3_PORT) --path $env(W3_PROJ)
expect {
    -re {\x1b\[} {}
    timeout { puts "FAIL-no-frames"; exit 1 }
}
sleep 3
log_user 1
send "w3-typed-e2e"
sleep 3
log_user 0
send "\x1d"
expect {
    eof { puts "ATTACH-DETACHED" }
    timeout { puts "FAIL-no-detach"; exit 1 }
}
EXPECT
chmod +x "$E2E_ROOT/attach.exp"
ATT_OUT="$(timeout 90 "$E2E_ROOT/attach.exp" 2>&1 || true)"
if grep -q "ATTACH-DETACHED" <<<"$ATT_OUT" && ! grep -q "FAIL" <<<"$ATT_OUT"; then
  record attach_flow PASS "rendered, typed, detached"
  pass "attach_flow: PTY attach rendered and detached"
else
  record attach_flow FAIL "$ATT_OUT"
  fail "attach_flow"
fi

TXT2="$(curl -s -m 10 "http://127.0.0.1:$PORT/s/$SESS_A/text")"
if grep -q "w3-typed-e2e" <<<"$TXT2"; then
  record attach_keys_reach_server PASS "typed text in the server grid"
  pass "attach_keys_reach_server"
else
  record attach_keys_reach_server FAIL "typed text never reached the server grid"
  fail "attach_keys_reach_server"
fi

HZ2="$(curl -s -m 5 "http://127.0.0.1:$PORT/sessions")"
if grep -q "$SESS_A" <<<"$HZ2"; then
  record attach_detach_survives PASS "session alive after detach"
  pass "attach_detach_survives"
else
  record attach_detach_survives FAIL "$HZ2"
  fail "attach_detach_survives"
fi

# ------------------------------------------------ plain server fallback
log "documented contract: a plain server answers /healthz; /connect is absent"
PLAIN_PORT=$((PORT + 2))
( cd "$PROJ_A" && exec "$GOA_BIN" server --server-addr "127.0.0.1:$PLAIN_PORT" \
    > "$E2E_ROOT/plain.log" 2>&1 ) & echo $! > "$E2E_ROOT/plain.pid"
for i in $(seq 1 50); do
  curl -s -m 1 "http://127.0.0.1:$PLAIN_PORT/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done
CODE_CONNECT="$(curl -s -o /dev/null -w '%{http_code}' -m 5 -X POST -H 'Content-Type: application/json' \
  -d "{\"path\":\"$PROJ_A\"}" "http://127.0.0.1:$PLAIN_PORT/connect")"
HZP="$(curl -s -m 5 "http://127.0.0.1:$PLAIN_PORT/healthz")"
if [ "$CODE_CONNECT" = "404" ] && grep -q '"session":"[0-9]' <<<"$HZP"; then
  record plain_fallback PASS "connect=404, healthz names the session"
  pass "plain_fallback"
else
  record plain_fallback FAIL "connect=$CODE_CONNECT healthz=$HZP"
  fail "plain_fallback"
fi
kill "$(cat "$E2E_ROOT/plain.pid")" 2>/dev/null || true

# ------------------------------------------------- max sessions (cap)
log "documented contract: --server-max-sessions caps live sessions"
stop_supervisor "$PORT"
start_supervisor "$((PORT + 1))" --server-projects-root "$ROOT" --server-max-sessions 1
PORT2=$((PORT + 1))
R1="$(connect_json "$PORT2" "$PROJ_A")"
R2="$(connect_json "$PORT2" "$PROJ_B")"
if grep -q '"session"' <<<"$R1" && grep -q "session limit reached" <<<"$R2"; then
  record max_sessions PASS "second project refused: $(python3 -c 'import sys,json;print(json.load(sys.stdin).get("error",""))' <<<"$R2")"
  pass "max_sessions: cap enforced with the documented error"
else
  record max_sessions FAIL "first=$R1 second=$R2"
  fail "max_sessions"
fi
stop_supervisor "$PORT2"


# ------------------------------------------------ idle reap
log "documented contract: --server-session-idle 2s reaps untouched sessions"
stop_supervisor "$PORT"
REAP_PORT=$((PORT + 3))
start_supervisor "$REAP_PORT" --server-projects-root "$ROOT" --server-session-idle 2s
R3="$(connect_json "$REAP_PORT" "$PROJ_A")"
SESS_R="$(python3 -c 'import sys,json;print(json.load(sys.stdin).get("session",""))' <<<"$R3")"
[ -n "$SESS_R" ] || { record idle_reap FAIL "no session"; fail "idle_reap: no session"; }
log "waiting 40s for the reaper tick (30s) + 2s idle..."
SUP_PID="$(cat "$E2E_ROOT/sup-$REAP_PORT.pid")"
for i in $(seq 1 8); do
  sleep 5
  if ps -p "$SUP_PID" >/dev/null 2>&1; then
    log "t+$((i*5))s supervisor alive"
  else
    log "t+$((i*5))s SUPERVISOR DIED"
    break
  fi
done
HZR="$(curl -s -m 5 "http://127.0.0.1:$REAP_PORT/healthz" || true)"
# grep exits 1 when nothing matches — the EXPECTED outcome here — and
# pipefail would turn that into a silent script death. Guard the grep.
NCHILDREN="$( (ps aux | grep "[g]oa server --server-addr unix://" || true) | wc -l | tr -d ' ')"
if grep -q '"sessions":0' <<<"$HZR" && [ "$NCHILDREN" = "0" ]; then
  record idle_reap PASS "reaped; transcript: $(ls "$PROJ_A/.goa/sessions/" 2>/dev/null | wc -l | tr -d ' ') on disk"
  pass "idle_reap: session reaped, child stopped, transcript kept"
else
  record idle_reap FAIL "healthz=$HZR children=$NCHILDREN"
  fail "idle_reap"
fi
stop_supervisor "$REAP_PORT"

# ------------------------------------------------ shutdown hygiene
# Grace: the final SIGTERM shuts the supervisor down gracefully (drain +
# child teardown), which can take a couple of seconds past the kill.
sleep 3
# Scoped to THIS run's surfaces: the suite's ports and the Unix-socket
# children. (A goa process from an unrelated session on this machine is not
# this suite's leak.)
RUN_PIDS="$(lsof -ti ":$PORT,$((PORT+1)),$((PORT+2)),$((PORT+3))" 2>/dev/null || true)"
LEFT="$( (ps aux | grep -E "[g]oa (server|attach)" | grep -E "127.0.0.1:$PORT|127.0.0.1:$((PORT+1))|127.0.0.1:$((PORT+2))|127.0.0.1:$((PORT+3))|unix://" || true) | wc -l | tr -d ' ')"
LEFT="$(( ${#RUN_PIDS} > 0 ? LEFT + 1 : LEFT ))"
if [ "$LEFT" != "0" ]; then
  LEFT_DETAIL="$(ps aux | grep -E "[g]oa (server|attach)" | grep -E "127.0.0.1:$PORT|unix://" | head -3)"
  sleep 3
  RUN_PIDS="$(lsof -ti ":$PORT,$((PORT+1)),$((PORT+2)),$((PORT+3))" 2>/dev/null || true)"
  LEFT="$( (ps aux | grep -E "[g]oa (server|attach)" | grep -E "127.0.0.1:$PORT|unix://" || true) | wc -l | tr -d ' ')"
  LEFT="$(( ${#RUN_PIDS} > 0 ? LEFT + 1 : LEFT ))"
fi
if [ "$LEFT" = "0" ]; then
  record shutdown_clean PASS "no goa processes left"
  pass "shutdown_clean"
else
  record shutdown_clean FAIL "$LEFT left: $LEFT_DETAIL"
  fail "shutdown_clean: $LEFT left"
fi

# ---------------------------------------------------------------- summary
log "results ($E2E_ROOT/results.tsv):"
cat "$E2E_ROOT/results.tsv"
if grep -q FAIL "$E2E_ROOT/results.tsv"; then
  exit 1
fi
exit 0
