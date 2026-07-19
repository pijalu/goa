// fetchers/zai.js — Z.ai (Zhipu GLM) quota via the monitor API (API key auth).
//
// Reports session (5h) and weekly windows plus web-search credits.

function fetch(ctx) {
	var apiKey = ctx.config.apiKey;
	if (!apiKey) {
		return { error: "no_api_key", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://api.z.ai";
	var resp = goa.http.fetch(base + "/api/monitor/usage/quota/limit", {
		method: "GET",
		headers: {
			"Authorization": "Bearer " + apiKey,
			"Accept": "application/json"
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
	var data = body.data || body;
	var limits = [];
	if (data.session) {
		limits.push({
			label: "Session (5h)",
			used: num(data.session.used),
			limit: num(data.session.limit),
			resetsAt: data.session.reset_at || null,
			periodMs: 5 * 3600000
		});
	}
	if (data.weekly) {
		limits.push({
			label: "Weekly",
			used: num(data.weekly.used),
			limit: num(data.weekly.limit),
			resetsAt: data.weekly.reset_at || null,
			periodMs: 7 * 86400000
		});
	}
	if (data.web_search) {
		limits.push({
			label: "Web searches",
			used: num(data.web_search.used),
			limit: num(data.web_search.limit),
			resetsAt: data.web_search.reset_at || null
		});
	}
	var plan = data.plan || (body.plan && body.plan.name) || null;
	return { plan: plan, limits: limits };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "Z.ai",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
