// fetchers/anthropic.js — Anthropic quota via the usage API (API key auth).
//
// Anthropic exposes account usage through a session/weekly windowed endpoint.
// Auth uses the same x-api-key as model access (no separate credential).

function fetch(ctx) {
	var apiKey = ctx.config.apiKey;
	if (!apiKey) {
		return { error: "no_api_key", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://api.anthropic.com";
	var resp = goa.http.fetch(base + "/v1/usage", {
		method: "GET",
		headers: {
			"x-api-key": apiKey,
			"anthropic-version": "2023-06-01"
		},
		timeoutMs: 15000
	});
	if (resp.error) {
		return { error: resp.error, plan: null, limits: [] };
	}
	if (resp.status === 401 || resp.status === 403) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	if (resp.status !== 200) {
		return { error: "http_" + resp.status, plan: null, limits: [] };
	}
	var body = parseJSON(resp.body);
	if (!body) {
		return { error: "bad_response", plan: null, limits: [] };
	}
	var usage = body.usage || body;
	var limits = [];
	if (usage.session) {
		limits.push({
			label: "Session (5h)",
			used: num(usage.session.used),
			limit: num(usage.session.limit),
			resetsAt: usage.session.reset_at || usage.session.resets_at || null
		});
	}
	if (usage.weekly) {
		limits.push({
			label: "Weekly",
			used: num(usage.weekly.used),
			limit: num(usage.weekly.limit),
			resetsAt: usage.weekly.reset_at || usage.weekly.resets_at || null
		});
	}
	var plan = (body.plan && body.plan.name) || body.plan || null;
	return { plan: plan, limits: limits };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "Anthropic",
	auth: { type: "api_key" },
	refreshInterval: 300000, // 5 min — usage changes slowly
	quotaEndpoint: true,
	fetch: fetch
};
