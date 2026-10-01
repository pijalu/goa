// fetchers/zai-coding-plan.js — z.ai Coding Plan RESET surface (quota credits).
//
// The z.ai quota fetcher (fetchers/zai.js) reports CONSUMPTION via the monitor
// API. This module covers the missing half: the Coding Plan reset credits that
// let a user clear an exhausted 5-hour or weekly window, which ZCode ships and
// goa did not (bugs.md #7).
//
// Endpoints (ZCode parity, all under the zcode.z.ai origin):
//   GET  /api/v1/coding-plan/reset/status          available resets + history
//   POST /api/v1/coding-plan/reset/opportunity     claim a grant (idempotency)
//   POST /api/v1/coding-plan/reset/use             consume FIVE_HOUR | WEEK
//   POST /api/v1/coding-plan/reset/history/read     clear the unread-history flag
//
// Contract details that MUST be preserved:
//
// 1. Business envelope {code, msg, data}. code 0 = success; **code 3301 =
//    throttled** and its data.next_try_at is the server's retry boundary. That
//    value is returned VERBATIM (nextTryAt) alongside the effective cooldown
//    we enforce (cooldownUntil); discarding it makes callers re-poll straight
//    back into the rate limit.
// 2. A bare HTTP 429 (no envelope, no next_try_at — the backend answers that
//    way when the request races the grant lock) maps to the SAME cooldown state
//    with a fallback duration, so a 30s poll cannot hammer the endpoint.
// 3. idempotency_key is MANDATORY and ≤64 chars on both mutating calls. An
//    invalid key fails locally: no request is sent.
// 4. Auth is DUAL: the zcodejwttoken (Bearer, `Authorization`) PLUS the family
//    OAuth access token (`X-Bigmodel-Authorization`, sent RAW — no Bearer
//    prefix). The family token is read for the account's own family only
//    (zai vs bigmodel): reading a fixed key breaks single-family logins, and
//    falling back to the other family would send the wrong identity to the
//    business API. There is NO cross-family fallback here, by contract.
//
// Cooldown scope: the persisted boundary suppresses the STATUS poll (the
// 60s scheduler and every quota refresh go through it), which is what keeps a
// 30s poll from hammering the endpoint. The mutating calls do not consult it —
// they are user-initiated, and plugin.js refuses them while the boundary is
// live. The module stays a primitive: it reports the boundary, it does not
// decide who may spend a credit.

var hq = require("../lib/http-quota.js");
var ids = require("../lib/ids.js");

// DEFAULT_ORIGIN is ZCode's own backend. The coding-plan API is NOT on the
// inference/monitor host (api.z.ai): both families issue their quota OAuth
// tokens from here, so this is where the reset surface lives.
var DEFAULT_ORIGIN = "https://zcode.z.ai";
var RESET_BASE_PATH = "/api/v1/coding-plan/reset";

// Credential/cooldown storage keys (plugin-namespaced: shared with the other
// bundled plugin instance only through goa.storage's per-plugin namespace).
var ZCODE_JWT_KEY = "zai.zcodejwttoken";
// OAUTH_TOKEN_KEY_PREFIX mirrors ZCode's credential keys so a token copied
// from there lands under the same name here.
var OAUTH_TOKEN_KEY_PREFIX = "oauth:";
var COOLDOWN_UNTIL_KEY = "zai.coding_plan.cooldown_until";
var COOLDOWN_NEXT_TRY_KEY = "zai.coding_plan.next_try_at";
var COOLDOWN_SOURCE_KEY = "zai.coding_plan.cooldown_source";

// MIN_RETRY_MS floors the enforced cooldown: even a tiny next_try_at must not
// turn into a hot poll loop (ZCode applies the same 5-minute floor).
var MIN_RETRY_MS = 5 * 60 * 1000;
// FALLBACK_COOLDOWN_MS is the cooldown for a bare 429, which carries no
// boundary of its own.
var FALLBACK_COOLDOWN_MS = 10 * 60 * 1000;
// GRANTED_COOLDOWN_MS: after a granted opportunity there is nothing to re-ask
// for until the grant has had a chance to be spent.
var GRANTED_COOLDOWN_MS = 10 * 60 * 1000;

var MAX_IDEMPOTENCY_KEY_LEN = 64;

var THROTTLE_CODE = 3301;
var SUCCESS_CODE = 0;

var RESET_TYPES = { FIVE_HOUR: "FIVE_HOUR", WEEK: "WEEK" };
// RESET_TYPE_ALIASES tolerates the spellings a user/completer can type.
var RESET_TYPE_ALIASES = {
	"five_hour": "FIVE_HOUR",
	"5h": "FIVE_HOUR",
	"five-hour": "FIVE_HOUR",
	"week": "WEEK",
	"weekly": "WEEK",
	"7d": "WEEK"
};

// --- public surface ---------------------------------------------------------

// status GETs the reset status. Inside an active cooldown it answers from
// storage WITHOUT touching the network: that is the whole point of persisting
// the boundary. Any error degrades to {error, status:null} so a quota render
// is never blocked by the reset surface.
function status(ctx) {
	if (throttled()) {
		return cooldownState();
	}
	var auth = resolveAuth(ctx);
	if (auth.error) {
		return auth;
	}
	return call(hq.getJSON, url(ctx, "status"), headersFor(auth, true), null, function(data) {
		return mapStatus(data);
	});
}

// requestOpportunity POSTs the claim for a reset grant. Returns
// {granted:true} or a throttle state carrying next_try_at verbatim. An
// omitted key mints a fresh one; an explicitly passed key (even an invalid
// one) is validated, so a bad key fails locally instead of silently becoming a
// different request.
function requestOpportunity(ctx, idempotencyKey) {
	var key = validateIdempotencyKey(mintIfAbsent(idempotencyKey, newIdempotencyKey));
	if (key.error) {
		return key;
	}
	var auth = resolveAuth(ctx);
	if (auth.error) {
		return auth;
	}
	return call(hq.postJSON, url(ctx, "opportunity"), headersFor(auth, true), { idempotency_key: key.value },
		function(data) {
			if (!data || data.granted !== true) {
				return { error: "coding_plan_reset_invalid_response" };
			}
			// Granted: nothing to re-ask for until the grant can be spent.
			setCooldown(Date.now() + GRANTED_COOLDOWN_MS, null, "granted");
			return { granted: true, cooldownUntil: cooldownUntil() };
		});
}

// useReset POSTs the consume call for one reset type (FIVE_HOUR | WEEK).
// idempotencyKey pins the key so a retry is deduped server-side; when omitted
// the module's retained key is used (minting one on the first attempt, keeping
// it until a terminal outcome so a retry re-sends the identical request).
function useReset(ctx, resetType, idempotencyKey) {
	var type = normalizeResetType(resetType);
	if (type.error) {
		return type;
	}
	var key = validateIdempotencyKey(mintIfAbsent(idempotencyKey, pendingKey));
	if (key.error) {
		return key;
	}
	var auth = resolveAuth(ctx);
	if (auth.error) {
		return auth;
	}
	return call(hq.postJSON, url(ctx, "use"), headersFor(auth, true),
		{ idempotency_key: key.value, reset_type: type.value },
		function(data) {
			if (!data || data.used !== true) {
				return { error: "coding_plan_reset_invalid_response" };
			}
			clearPendingKey(); // terminal outcome — the next use mints a fresh key
			clearCooldown();
			return { used: true, resetType: type.value };
		});
}

// markHistoryRead POSTs the read-cursor clear. The call authenticates with the
// same dual credentials but carries NO target scope: the cursor is per user,
// not per plan scope.
function markHistoryRead(ctx) {
	var auth = resolveAuth(ctx);
	if (auth.error) {
		return auth;
	}
	return call(hq.postJSON, url(ctx, "history/read"), headersFor(auth, false), {},
		function() {
			return { markedRead: true };
		});
}

// --- mapping ----------------------------------------------------------------

// mapStatus maps the status payload onto the camelCase shape the rest of the
// plugin speaks:
//   {availableFiveHour:[{expireAtMs}], availableWeek:[{expireAtMs}],
//    latestFiveHourUsedAtMs, latestWeekUsedAtMs, hasUnreadHistory}
// Tolerantly: a missing/!Array field reads as an empty list, and unusable
// entries (no positive expire_at) are dropped rather than rendered as "now".
function mapStatus(data) {
	var d = data || {};
	return {
		availableFiveHour: opportunities(d.available_five_hour_resets),
		availableWeek: opportunities(d.available_week_resets),
		latestFiveHourUsedAtMs: usedAt(d.latest_five_hour_reset_history),
		latestWeekUsedAtMs: usedAt(d.latest_week_reset_history),
		hasUnreadHistory: d.has_unread_history === true,
		availableCount: opportunities(d.available_five_hour_resets).length +
			opportunities(d.available_week_resets).length
	};
}

// opportunities maps one available-resets array, dropping entries without a
// positive expiry timestamp.
function opportunities(raw) {
	var out = [];
	if (!Array.isArray(raw)) {
		return out;
	}
	for (var i = 0; i < raw.length; i++) {
		var item = raw[i] || {};
		var ms = hq.num(item.expire_at);
		if (ms > 0) {
			out.push({ expireAtMs: ms });
		}
	}
	return out;
}

// usedAt reads one latest-history entry's used_at, or 0 when absent.
function usedAt(h) {
	if (!h) {
		return 0;
	}
	return Math.max(0, hq.num(h.used_at));
}

// --- transport / envelope ---------------------------------------------------

// call performs one coding-plan request through the shared HTTP funnel and
// unwraps the {code,msg,data} envelope. onData receives the unwrapped data
// (or a throttle state) and its result is returned untouched.
//
// Throttle handling lives here so every endpoint honours both 3301 and 429:
//   3301 → next_try_at passed through VERBATIM, cooldown floored at MIN_RETRY_MS
//   429  → same cooldown state with FALLBACK_COOLDOWN_MS (no server boundary)
function call(helper, urlStr, headers, bodyObj, onData) {
	var out = bodyObj === null
		? helper(urlStr, headers, function(body) { return unwrap(body, onData); })
		: helper(urlStr, headers, bodyObj, function(body) { return unwrap(body, onData); });
	if (out && out.error === "http_429") {
		// Bare 429: the backend throttled without an envelope, so synthesize the
		// same cooldown from the fallback duration.
		setCooldown(Date.now() + FALLBACK_COOLDOWN_MS, null, "429");
		return cooldownState();
	}
	return out;
}

// unwrap turns the business envelope into either a throttle state (3301), an
// error (non-zero code, unknown shape), or onData(data).
function unwrap(body, onData) {
	if (!body || typeof body !== "object") {
		return { error: "coding_plan_reset_invalid_response" };
	}
	var code = body.code;
	if (code === THROTTLE_CODE) {
		var nextTryAt = body.data ? hq.num(body.data.next_try_at) : 0;
		return throttleState(nextTryAt, "next_try_at");
	}
	if (code !== SUCCESS_CODE) {
		return { error: "coding_plan_reset_api_error:" + String(code) };
	}
	return onData(body.data);
}

// --- auth -------------------------------------------------------------------

// resolveAuth builds the DUAL credential pair for the coding-plan API.
// Failure modes are explicit and never degrade into a substitute credential:
//   zcode_jwt missing → coding_plan_reset_zcode_jwt_required
//   family token missing → coding_plan_reset_maas_jwt_required
// Both map to auth_required for the caller's error surface.
//
// The Goa-managed credential is fetched ONCE and used for both halves: the pair
// is minted together by a single /login, so reading them separately could pair a
// fresh access token with a stale JWT (or the reverse) and produce a confusing
// server-side rejection.
function resolveAuth(ctx) {
	var config = (ctx && ctx.config) || {};
	var family = familyOf(config);
	var managed = goaOAuthCredential(family);
	var zcodeJwt = trim(zcodeJwtOf(config, managed));
	if (!zcodeJwt) {
		return authError("coding_plan_reset_zcode_jwt_required");
	}
	var familyToken = familyAccessToken(config, family, managed);
	if (!familyToken) {
		return authError("coding_plan_reset_maas_jwt_required");
	}
	return { family: family, zcodeJwt: zcodeJwt, familyToken: familyToken };
}

// authError marks a credential failure as auth_required while keeping the
// specific reason (so /quota can explain which credential is missing).
function authError(reason) {
	return { error: "auth_required", reason: reason, throttled: false, nextTryAt: 0, status: null };
}

// headersFor builds the request headers. The zcode JWT goes out as a Bearer
// token (prefixed only when the stored value lacks it); the family OAuth token
// is sent RAW, per the backend contract. includeTargetScope adds the personal
// plan scope; history/read omits it (the read cursor is per user).
function headersFor(auth, includeTargetScope) {
	var headers = {
		"Authorization": /^Bearer\s/i.test(auth.zcodeJwt) ? auth.zcodeJwt : "Bearer " + auth.zcodeJwt,
		"X-Bigmodel-Authorization": auth.familyToken,
		"Accept": "application/json"
	};
	if (includeTargetScope) {
		headers["Bigmodel-Target-Type"] = "PERSONAL";
	}
	return headers;
}

// familyOf classifies the account as "zai" or "bigmodel". Goa has no separate
// bigmodel provider id: the CN coding plan is the SAME provider identity
// pointed at the bigmodel host, so the endpoint decides the family. Only this
// family's OAuth token may be used — the reset API validates the token
// against the family of the account.
function familyOf(config) {
	var hay = String(
		(config.provider || "") + " " + (config.baseUrl || "") + " " + (config.endpoint || "")
	).toLowerCase();
	return hay.indexOf("bigmodel") >= 0 || hay.indexOf("zhipu") >= 0 ? "bigmodel" : "zai";
}

// mintIfAbsent returns mint() only when key was not supplied (null/undefined).
// An explicitly passed value — including "" — flows into validation, so an
// invalid key is reported instead of being replaced by a different request.
function mintIfAbsent(key, mint) {
	return key === undefined || key === null ? mint() : key;
}

// familyAccessToken reads the account's OWN family OAuth access token, in
// order: the provider config, the plugin storage key ZCode uses
// (`oauth:<family>:access_token`), then Goa's managed OAuth credential from
// /login:<family>:oauth. There is deliberately no fallback to the OTHER family:
// that token is a different identity, and the reset API validates the token
// against the family of the account.
function familyAccessToken(config, family, managed) {
	return trim(
		config.accessToken ||
		goa.storage.get(OAUTH_TOKEN_KEY_PREFIX + family + ":access_token") ||
		(managed ? managed.accessToken : "")
	);
}

// goaOAuthCredential reads the Goa-managed Coding Plan credential for a family,
// returning null when there is none.
//
// This is the /login:zai:oauth path: Goa mints BOTH credentials in one login and
// hands them back together ({accessToken, zcodeJwt}). Returning null when the
// bridge errors is correct here — "no managed credential" is exactly the absent
// state this function models, and resolveAuth turns it into the specific
// missing-credential reason for /quota to display.
function goaOAuthCredential(family) {
	if (!goa.auth || typeof goa.auth.oauthToken !== "function") {
		return null;
	}
	var tok = goa.auth.oauthToken(family);
	if (!tok || tok.error || !tok.accessToken) {
		return null;
	}
	return tok;
}

// zcodeJwtOf reads the zcode business JWT, in precedence order: the provider
// config (an explicit override always wins), the plugin storage key ZCode uses,
// then Goa's managed credential from /login — which is what makes
// `/login:zai:oauth` sufficient on its own, with no config editing.
function zcodeJwtOf(config, managed) {
	return config.zcodeJwt || config.zcodejwttoken ||
		goa.storage.get(ZCODE_JWT_KEY) ||
		(managed ? trim(managed.zcodeJwt) : "") || "";
}

// --- validation -------------------------------------------------------------

// validateIdempotencyKey enforces the mandatory, ≤64-char contract. A bad key
// fails here: no request goes out (the backend would reject it anyway, and a
// rejected call is a wasted rate-limit slot).
function validateIdempotencyKey(key) {
	var k = trim(key);
	if (k === "") {
		return { error: "coding_plan_reset_invalid_idempotency_key" };
	}
	if (k.length > MAX_IDEMPOTENCY_KEY_LEN) {
		return { error: "coding_plan_reset_invalid_idempotency_key" };
	}
	return { value: k };
}

// normalizeResetType accepts FIVE_HOUR | WEEK plus the spellings a user can
// type (five_hour, 5h, weekly, ...). Unknown types fail before any request.
function normalizeResetType(v) {
	var s = trim(v).toUpperCase().replace(/-/g, "_");
	if (RESET_TYPES[s]) {
		return { value: RESET_TYPES[s] };
	}
	var alias = RESET_TYPE_ALIASES[s.toLowerCase()];
	if (alias) {
		return { value: alias };
	}
	return { error: "coding_plan_reset_invalid_reset_type" };
}

// --- idempotency keys -------------------------------------------------------

// _pendingUseKey is the retained use-reset key, cleared only on a terminal
// outcome so a retry re-sends the identical request (the server dedupes a
// double-consume).
var _pendingUseKey = null;

function pendingKey() {
	if (!_pendingUseKey) {
		_pendingUseKey = ids.uuidV4();
	}
	return _pendingUseKey;
}

function clearPendingKey() {
	_pendingUseKey = null;
}

function newIdempotencyKey() {
	return ids.uuidV4();
}

// --- cooldown ---------------------------------------------------------------

// throttleState builds the canonical throttle result and PERSISTS the boundary
// so the next status() call answers from storage instead of re-polling.
// nextTryAt (the server's verbatim value, 0 when it sent none) is kept
// distinct from cooldownUntil (the boundary we actually enforce, floored at
// MIN_RETRY_MS) so neither is lost in translation.
function throttleState(nextTryAt, source) {
	var raw = nextTryAt > 0 ? nextTryAt : 0;
	var until = raw > 0 ? Math.max(raw, Date.now() + MIN_RETRY_MS) : Date.now() + FALLBACK_COOLDOWN_MS;
	setCooldown(until, raw, source);
	return {
		throttled: true,
		nextTryAt: raw,
		cooldownUntil: until,
		source: source,
		error: "coding_plan_reset_throttled"
	};
}

// setCooldown persists the enforced boundary (plus the verbatim server value
// and its source) for the next call to honor.
function setCooldown(until, nextTryAt, source) {
	goa.storage.set(COOLDOWN_UNTIL_KEY, String(until));
	if (nextTryAt > 0) {
		goa.storage.set(COOLDOWN_NEXT_TRY_KEY, String(nextTryAt));
	} else {
		goa.storage.delete(COOLDOWN_NEXT_TRY_KEY);
	}
	goa.storage.set(COOLDOWN_SOURCE_KEY, String(source || ""));
}

// cooldownUntil returns the persisted boundary, or 0 when none/expired.
function cooldownUntil() {
	var until = hq.num(goa.storage.get(COOLDOWN_UNTIL_KEY));
	return until > Date.now() ? until : 0;
}

// throttled reports whether an unexpired cooldown is persisted.
function throttled() {
	return cooldownUntil() > 0;
}

// cooldownState rebuilds the throttle result from storage — the answer given
// while a cooldown is active, without a request.
function cooldownState() {
	return {
		throttled: true,
		nextTryAt: hq.num(goa.storage.get(COOLDOWN_NEXT_TRY_KEY)),
		cooldownUntil: cooldownUntil(),
		source: goa.storage.get(COOLDOWN_SOURCE_KEY) || "cooldown",
		error: "coding_plan_reset_throttled"
	};
}

// clearCooldown drops the persisted boundary (a successful use clears it).
function clearCooldown() {
	goa.storage.delete(COOLDOWN_UNTIL_KEY);
	goa.storage.delete(COOLDOWN_NEXT_TRY_KEY);
	goa.storage.delete(COOLDOWN_SOURCE_KEY);
}

// --- helpers ----------------------------------------------------------------

// url builds one reset endpoint URL under the configured (or default) origin.
function url(ctx, path) {
	var config = (ctx && ctx.config) || {};
	var base = trimSlash(config.codingPlanBaseUrl || config.zcodeBaseUrl || DEFAULT_ORIGIN);
	return base + RESET_BASE_PATH + "/" + path;
}

function trim(v) {
	return String(v == null ? "" : v).trim();
}

function trimSlash(s) {
	return String(s).replace(/\/+$/, "");
}

module.exports = {
	name: "Z.ai Coding Plan Resets",
	status: status,
	requestOpportunity: requestOpportunity,
	useReset: useReset,
	markHistoryRead: markHistoryRead,
	// Exported for unit tests pinning the contract (mapping, throttle,
	// validation, auth resolution, cooldown persistence).
	mapStatus: mapStatus,
	throttleState: throttleState,
	resolveAuth: resolveAuth,
	familyOf: familyOf,
	headersFor: headersFor,
	validateIdempotencyKey: validateIdempotencyKey,
	normalizeResetType: normalizeResetType,
	cooldownUntil: cooldownUntil,
	clearCooldown: clearCooldown,
	pendingKey: pendingKey,
	clearPendingKey: clearPendingKey,
	urls: { status: "/status", opportunity: "/opportunity", use: "/use", historyRead: "/history/read" },
	constants: {
		DEFAULT_ORIGIN: DEFAULT_ORIGIN,
		RESET_BASE_PATH: RESET_BASE_PATH,
		MIN_RETRY_MS: MIN_RETRY_MS,
		FALLBACK_COOLDOWN_MS: FALLBACK_COOLDOWN_MS,
		GRANTED_COOLDOWN_MS: GRANTED_COOLDOWN_MS,
		MAX_IDEMPOTENCY_KEY_LEN: MAX_IDEMPOTENCY_KEY_LEN,
		THROTTLE_CODE: THROTTLE_CODE,
		ZCODE_JWT_KEY: ZCODE_JWT_KEY
	}
};
