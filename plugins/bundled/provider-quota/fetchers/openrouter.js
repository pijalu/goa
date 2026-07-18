// fetchers/openrouter.js — OpenRouter quota via the key-info API (API key auth).
//
// OpenRouter exposes credit balance on the API key itself.

function fetch(ctx) {
	var apiKey = ctx.config.apiKey;
	if (!apiKey) {
		return { error: "no_api_key", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://openrouter.ai";
	var resp = goa.http.fetch(base + "/api/v1/auth/key", {
		method: "GET",
		headers: { "Authorization": "Bearer " + apiKey },
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
	if (!body || !body.data) {
		return { error: "bad_response", plan: null, limits: [] };
	}
	var d = body.data;
	var limits = [];
	// limit_remaining / limit are in USD credits.
	if (d.limit !== undefined && d.limit !== null) {
		limits.push({
			label: "Credits",
			used: Math.round(num(d.usage) * 100),          // cents
			limit: Math.round(num(d.limit) * 100),          // cents
			resetsAt: null
		});
	} else if (d.limit_remaining !== undefined) {
		limits.push({
			label: "Credits remaining",
			used: 0,
			limit: 0,
			remaining: num(d.limit_remaining),
			resetsAt: null
		});
	}
	return { plan: d.label || null, limits: limits, costUnit: "usd_credits" };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "OpenRouter",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
