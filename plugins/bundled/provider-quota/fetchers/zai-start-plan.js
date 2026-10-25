// fetchers/zai-start-plan.js — Z.ai / BigModel Start Plan quota (free tier).
//
// Start Plan is the free GLM tier served by the zcode plan backend. Its quota
// surface is billing/balance (ZCode parity, zaiStartPlanBilling.ts):
//
//	GET {runtime-base}/billing/balance?app_version=<version>
//	Authorization: Bearer <zcode JWT>     — the /login:<family>:oauth credential
//	X-Device-Mid: <deviceMid>             — REQUIRED; missing -> 400 code 3001
//
// The module is a FACTORY: plugin.js registers one instance per family
// ("zai-start-plan" / "bigmodel-start-plan"), because authAvailable() is
// per-fetcher-object and each family checks its OWN login token — a zai
// login must never light up the bigmodel row.
//
// Credential path: the family login (/login:<family>:oauth) plus the JWT the
// app resolves into ctx.config.apiKey. The app's pluginProvidersMap resolves
// it through ProviderManager.ResolveAPIKey, which for Start Plan providers
// returns the family zcode JWT — the same identity the inference runtime
// sends, so quota and requests can never disagree about the account.
//
// Mapping rules (mirror the Go startplan client — one contract, two readers):
//   - envelope {code,msg,data}: code 0/200/absent (and success!==false) = OK;
//     anything else is a business error surfaced verbatim as
//     "start_plan_error_<code>" so /quota explains itself.
//   - an "active" plan past ends_at is expired; buckets owned ONLY by expired
//     plans are dropped; unattributed buckets are KEPT (deleting a bucket that
//     belongs to another active instance would hide quota the user owns).
//   - no models AND every entitlement effective_at strictly in the future ->
//     one "Pending" row (a pending plan is information, not an absence).
//   - zero buckets otherwise -> one explicit "No active quota" row: a Start
//     Plan row must never silently vanish from /quota.
//   - expires_at is unix SECONDS; format.toMs() auto-detects the scale.

var hq = require("../lib/http-quota.js");

// DEFAULT_BASE is the zcode plan runtime base. The balance route hangs
// directly under it: {origin}/api/v1 + /zcode-plan + /billing/balance.
var DEFAULT_BASE = "https://zcode.z.ai/api/v1/zcode-plan";

// APP_VERSION mirrors the goa placeholder version sent by the Go runtime
// (userAgent + startplan.AppVersion). goa carries no build-stamped version,
// so the honest identity is the placeholder, not a fabricated number.
var APP_VERSION = "0.0.0";

// family is NOT module state: the require cache means both family instances
// share this module scope, so a mutable "current family" variable would let
// the second factory call silently re-point the first instance. Every helper
// that needs the family receives it as an explicit argument, and the factory
// closes over its own copy.

module.exports = function startPlanFetcher(family) {
	return {
		name: family === "bigmodel" ? "BigModel Start Plan" : "Z.ai Start Plan",
		// goa_oauth auth: authAvailable() checks the FAMILY login token, so a
		// family without /login renders an auth-required row (with the right
		// login hint) instead of firing a doomed request.
		auth: { type: "goa_oauth", provider: family },
		refreshInterval: 300000,
		quotaEndpoint: true,
		fetch: function(ctx) {
			return fetchBalance(family, ctx);
		}
	};
};

// fetchBalance runs one balance request and maps it onto the shared
// {plan, limits} result. The hq.runFetch skeleton is not used because the
// device-mid gate and the business envelope both need provider-specific
// handling that the generic descriptor cannot express.
function fetchBalance(family, ctx) {
	if (!familyToken(family)) {
		// authAvailable() gates refreshes on this same token; the fetcher must
		// agree — refreshAllDue still CALLS the fetcher to cache the
		// auth_required state, and a family that never ran /login must not
		// spend its config key on a doomed request (family isolation).
		return { error: "auth_required", plan: null, limits: [] };
	}
	var config = (ctx && ctx.config) || {};
	var jwt = String(config.apiKey || "").trim();
	if (!jwt) {
		// Logged in but the app has not resolved a credential yet: keep the
		// safe degrade (auth_required — the credential comes from /login, not
		// a pasted provider key).
		return { error: "auth_required", plan: null, limits: [] };
	}
	var mid = deviceMid();
	if (!mid) {
		return { error: "start_plan_device_mid_required", plan: null, limits: [] };
	}
	var headers = {
		"Authorization": /^Bearer\s/i.test(jwt) ? jwt : "Bearer " + jwt,
		"X-Device-Mid": mid,
		"Accept": "application/json"
	};
	return hq.getJSON(balanceURL(config), headers, function(body) {
		return mapBalance(body);
	});
}

// balanceURL builds {runtime-base}/billing/balance?app_version=<v>. The
// runtime base IS origin + /zcode-plan, so the balance route hangs straight
// under the configured endpoint (strip a trailing /billing/balance so a
// previously-copied full URL still resolves).
function balanceURL(config) {
	var base = trimSlash(config.baseUrl || config.endpoint || DEFAULT_BASE);
	base = base.replace(/\/billing\/balance$/, "");
	return base + "/billing/balance?app_version=" + encodeURIComponent(APP_VERSION);
}

// deviceMid reads the per-install device identity from the goa bridge. An
// unavailable bridge (older host) degrades to an explicit error row — never
// to a headerless request the backend would answer with 400 code 3001.
function deviceMid() {
	if (goa.auth && typeof goa.auth.deviceMid === "function") {
		return String(goa.auth.deviceMid() || "");
	}
	return "";
}

// familyToken returns the FAMILY's Goa login token, or null when that family
// never authenticated — the exact check authAvailable() performs, mirrored so
// the fetch and the gate can never disagree about "logged in".
function familyToken(family) {
	if (goa.auth && typeof goa.auth.oauthToken === "function") {
		var tok = goa.auth.oauthToken(family);
		if (tok && tok.accessToken) {
			return tok;
		}
	}
	return null;
}

// mapBalance turns the balance envelope into quota rows (see module header
// for the contract).
function mapBalance(body) {
	if (!body || typeof body !== "object") {
		return { error: "bad_response", plan: null, limits: [] };
	}
	var code = body.code;
	var ok = code === undefined || code === null || code === 0 || code === 200;
	if (body.success === false) {
		ok = false;
	}
	if (!ok) {
		return { error: "start_plan_error_" + code, plan: null, limits: [] };
	}
	var data = body.data || {};
	var nowSec = Math.floor(Date.now() / 1000);

	var plans = Array.isArray(data.plans) ? data.plans : [];
	var balances = Array.isArray(data.balances) ? data.balances : [];

	// Expiry normalization against the server clock when it sent one
	// (server_time), else local time — a skewed client clock must not keep
	// a dead plan's buckets alive.
	var clockSec = hq.num(data.server_time);
	if (clockSec <= 0) {
		clockSec = nowSec;
	}
	var live = [];
	for (var i = 0; i < plans.length; i++) {
		var p = plans[i] || {};
		if (String(p.status || "").toLowerCase() !== "active") {
			continue;
		}
		var ends = hq.num(p.ends_at);
		if (ends > 0 && ends <= clockSec) {
			continue; // expired despite the stale "active" label
		}
		live.push(p);
	}

	var limits = [];
	for (var j = 0; j < balances.length; j++) {
		var row = bucketRow(balances[j] || {}, plans, live, clockSec);
		if (row) {
			limits.push(row);
		}
	}

	if (limits.length === 0) {
		// Pending beats "empty": a plan whose entitlements all start in the
		// future is information the user can act on.
		var pend = pendingAt(plans, clockSec);
		if (pend > 0) {
			return {
				plan: planLabel(plans),
				limits: [{ label: "Pending (starts " + fmtDate(pend) + ")", used: 0, limit: 0, resetsAt: pend * 1000, periodMs: 0 }]
			};
		}
		return {
			plan: planLabel(plans),
			limits: [{ label: "No active quota", used: 0, limit: 0, resetsAt: null, periodMs: 0 }]
		};
	}
	return { plan: planLabel(plans), limits: limits };
}

// bucketRow maps one quota bucket onto a /quota row, or null when it carries
// no usable numbers. Ownership follows user_plan_id (fallback plan_id); a
// bucket owned ONLY by expired plans is dropped, an unattributed bucket is
// kept (module header, mapping rules).
function bucketRow(b, plans, live, clockSec) {
	if (!ownedByLivePlan(b, plans, live)) {
		return null;
	}
	var total = hq.num(b.total_units);
	var remaining = hq.num(b.remaining_units);
	var models = bucketModels(b);
	var label = models.length > 0 ? models.join(", ") : (String(b.show_name || "").trim() || String(b.bucket_id || "Quota"));
	var expiresAt = hq.num(b.expires_at);
	var expired = expiresAt > 0 && expiresAt <= clockSec;

	if (expired) {
		// The window has run out: show it spent, not missing — a row at 100%
		// with its reset time explains the state; an absent row looks broken.
		if (total <= 0 && remaining <= 0) {
			return null;
		}
		var spent = total > 0 ? total : remaining;
		return { label: label, used: spent, limit: spent, resetsAt: expiresAt * 1000, periodMs: 0 };
	}
	if (total > 0) {
		var used = total - remaining;
		if (used < 0) {
			used = 0;
		}
		return { label: label, used: used, limit: total, resetsAt: expiresAt > 0 ? expiresAt * 1000 : null, periodMs: 0 };
	}
	if (remaining > 0) {
		// Only a remaining count: surface it as an absolute (limit 0 renders
		// the raw number, per the renderLimitRow unbounded branch).
		return { label: label, used: remaining, limit: 0, resetsAt: expiresAt > 0 ? expiresAt * 1000 : null, periodMs: 0 };
	}
	return null;
}

// ownedByLivePlan keeps unattributed buckets and buckets with at least one
// live owner; drops buckets owned only by expired plans.
function ownedByLivePlan(b, plans, live) {
	var owners = [];
	for (var i = 0; i < plans.length; i++) {
		var p = plans[i] || {};
		if (b.user_plan_id && p.user_plan_id) {
			if (p.user_plan_id === b.user_plan_id) {
				owners.push(p);
			}
			continue;
		}
		if (b.plan_id && p.plan_id && p.plan_id === b.plan_id) {
			owners.push(p);
		}
	}
	if (owners.length === 0) {
		return true; // unattributed: keep (backend promises attribution)
	}
	for (var j = 0; j < owners.length; j++) {
		for (var k = 0; k < live.length; k++) {
			if (owners[j] === live[k]) {
				return true;
			}
		}
	}
	return false;
}

// bucketModels extracts the servable model ids from bucket capabilities
// ("model:<id>" entries), trimmed, in order.
function bucketModels(b) {
	var caps = Array.isArray(b.capabilities) ? b.capabilities : [];
	var out = [];
	for (var i = 0; i < caps.length; i++) {
		var c = String(caps[i] || "").trim();
		if (c.toLowerCase().indexOf("model:") !== 0) {
			continue;
		}
		var id = c.slice("model:".length).trim();
		if (id) {
			out.push(id);
		}
	}
	return out;
}

// pendingAt returns the earliest entitlement effective time (fallback
// starts_at) when it is strictly in the future, else 0. effective_at = 0
// means "immediate" and never pends.
function pendingAt(plans, clockSec) {
	var best = 0;
	for (var i = 0; i < plans.length; i++) {
		var p = plans[i] || {};
		if (String(p.status || "").toLowerCase() !== "active") {
			continue;
		}
		var times = [];
		var ents = Array.isArray(p.entitlements) ? p.entitlements : [];
		if (ents.length === 0) {
			var s = hq.num(p.starts_at);
			if (s > 0 || (p.starts_at !== undefined && p.starts_at !== null && hq.num(p.starts_at) === 0)) {
				times.push(hq.num(p.starts_at));
			}
		}
		for (var j = 0; j < ents.length; j++) {
			times.push(hq.num((ents[j] || {}).effective_at));
		}
		for (var k = 0; k < times.length; k++) {
			var t = times[k];
			if (t > clockSec && (best === 0 || t < best)) {
				best = t;
			}
		}
	}
	return best;
}

// planLabel names the plan for the row header ("Z.ai Start Plan (Start)").
function planLabel(plans) {
	for (var i = 0; i < plans.length; i++) {
		var p = plans[i] || {};
		var name = String(p.name || p.plan_id || "").trim();
		if (name) {
			return name;
		}
	}
	return null;
}

function fmtDate(sec) {
	try {
		var d = new Date(sec * 1000);
		return d.toISOString().slice(0, 10);
	} catch (e) {
		return String(sec);
	}
}

function trimSlash(s) {
	return String(s == null ? "" : s).replace(/\/+$/, "");
}

// Exported for unit tests pinning the contract (mapping, ownership, pending).
module.exports._internal = {
	balanceURL: balanceURL,
	mapBalance: mapBalance,
	bucketRow: bucketRow,
	ownedByLivePlan: ownedByLivePlan,
	bucketModels: bucketModels,
	pendingAt: pendingAt,
	planLabel: planLabel,
	familyToken: familyToken,
	APP_VERSION: APP_VERSION,
	DEFAULT_BASE: DEFAULT_BASE
};
