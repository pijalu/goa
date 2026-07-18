// fetchers/minimax.js — MiniMax quota via the token-plan API (API key auth).

function fetch(ctx) {
	var apiKey = ctx.config.apiKey;
	if (!apiKey) {
		return { error: "no_api_key", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://api.minimax.io";
	var resp = goa.http.fetch(base + "/v1/token_plan/remains", {
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
	if (!body) {
		return { error: "bad_response", plan: null, limits: [] };
	}
	var data = body.data || body;
	var limits = [];
	if (data.session) {
		limits.push({
			label: "Session",
			used: num(data.session.used),
			limit: num(data.session.limit),
			resetsAt: data.session.reset_at || null
		});
	} else if (data.remains !== undefined) {
		limits.push({
			label: "Tokens remaining",
			used: num(data.used),
			limit: num(data.remains) + num(data.used),
			resetsAt: data.reset_at || null
		});
	}
	return { plan: data.plan || null, limits: limits };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "MiniMax",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
