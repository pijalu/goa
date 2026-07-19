// fetchers/opencode.js — OpenCode quota via OAuth + console usage API.
//
// OpenCode uses OAuth device-code auth for its console, separate from model
// access. Token lifecycle is owned by lib/oauth.js.
//
// The console exposes usage EVENTS (not quota windows): GET /api/usage/rows
// returns per-request rows carrying costMicroCents, scoped to an org via the
// x-org-id header (discovered from GET /api/orgs). OpenCode Go's published
// plan limits are fixed ($12 / 5h, $30 / 7d, $60 / 30d), so the quota bars
// are computed as observed spend over each window against those limits — the
// same model OpenUsage documents for this provider.

var oauth = require("../lib/oauth.js");
var hq = require("../lib/http-quota.js");

var AUTH = {
	type: "oauth",
	clientId: "goa-plugin",
	authUrl: "https://console.opencode.ai/auth/device/code",
	tokenUrl: "https://console.opencode.ai/auth/device/token",
	verificationUri: "https://console.opencode.ai/device"
};

var BASE = "https://console.opencode.ai";

// Plan windows: [label, rangeParam, windowMs, limitDollars]. The console's
// range enum is 24h/7d/30d; the plan's 5h session window is computed by
// pulling the 24h range and filtering rows to the last 5h by timestamp.
var WINDOWS = [
	["Session (5h)", "24h", 5 * 3600000, 12],
	["Weekly (7d)", "7d", 7 * 86400000, 30],
	["Monthly (30d)", "30d", 30 * 86400000, 60]
];

var MAX_PAGES = 20; // 20 × 100 rows = 2000 events per window, plenty

function fetch(ctx) {
	var token = oauth.getToken("opencode", AUTH);
	if (!token) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	var orgId = discoverOrg(token);
	if (!orgId) {
		return { error: "no_org", plan: null, limits: [] };
	}
	var now = Date.now();
	var limits = [];
	for (var i = 0; i < WINDOWS.length; i++) {
		var w = WINDOWS[i];
		var microCents = sumWindowSpend(token, orgId, w[1], now - w[2]);
		if (microCents < 0) {
			return { error: "usage_fetch_failed", plan: null, limits: [] };
		}
		limits.push({
			label: w[0],
			used: Math.round(microCents / 10000), // micro-cents → cents
			limit: w[3] * 100, // dollars → cents
			resetsAt: null,
			periodMs: w[2]
		});
	}
	return { plan: "OpenCode Go", limits: limits, costUnit: "cents" };
}

// discoverOrg returns the first org id the token can see, cached in storage
// so routine refreshes stay a single request per window.
function discoverOrg(token) {
	var cached = goa.storage.get("opencode.org_id");
	if (cached) {
		return cached;
	}
	var orgs = hq.getJSON(BASE + "/api/orgs", authHeaders(token, null), identity);
	if (!orgs || !orgs.length || !orgs[0].id) {
		return null;
	}
	goa.storage.set("opencode.org_id", orgs[0].id);
	return orgs[0].id;
}

// sumWindowSpend pages /api/usage/rows and sums costMicroCents for events at
// or after sinceMs. Returns -1 on a transport/auth failure so the caller can
// surface an error instead of a bogus zero.
function sumWindowSpend(token, orgId, range, sinceMs) {
	var total = 0;
	var cursor = null;
	for (var page = 0; page < MAX_PAGES; page++) {
		var url = BASE + "/api/usage/rows?scope=organization&range=" + range + "&pageSize=100";
		if (cursor) {
			url += "&cursor=" + encodeURIComponent(cursor);
		}
		var body = hq.getJSON(url, authHeaders(token, orgId), identity);
		if (!body || body.error !== undefined || !body.items) {
			return -1;
		}
		var done = accumulate(body.items, sinceMs, function(c) { total += c; });
		if (done) {
			return total;
		}
		cursor = body.nextCursor;
		if (!cursor) {
			return total;
		}
	}
	return total;
}

// accumulate adds each row's costMicroCents to add() when the row is inside
// the window. Rows are newest-first, so the first row older than sinceMs ends
// the scan (returns true).
function accumulate(items, sinceMs, add) {
	for (var i = 0; i < items.length; i++) {
		var row = items[i];
		var created = Date.parse(row.createdAt || row.lastUsedAt || "");
		if (!isNaN(created) && created < sinceMs) {
			return true;
		}
		add(hq.num(row.costMicroCents));
	}
	return false;
}

// authHeaders builds the Bearer + optional org header for console API calls.
function authHeaders(token, orgId) {
	var headers = { "Authorization": "Bearer " + token, "Accept": "application/json" };
	if (orgId) {
		headers["x-org-id"] = orgId;
	}
	return headers;
}

function identity(b) { return b; }

module.exports = {
	name: "OpenCode",
	auth: AUTH,
	refreshInterval: 300000, // 5 min — spend changes slowly
	quotaEndpoint: true,
	fetch: fetch
};
