// fetchers/kimi.js — Kimi (Moonshot) quota via the coding usages API (OAuth).
//
// Kimi uses OAuth bearer tokens for its quota console, separate from the API
// key used for inference. Token lifecycle is owned by lib/oauth.js.

var oauth = require("../lib/oauth.js");

var AUTH = {
	type: "oauth",
	clientId: "goa-plugin",
	authUrl: "https://platform.moonshot.ai/oauth/device/code",
	tokenUrl: "https://platform.moonshot.ai/oauth/device/token",
	verificationUri: "https://platform.moonshot.ai/oauth/device"
};

function fetch(ctx) {
	var token = oauth.getToken("kimi", AUTH);
	if (!token) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://api.moonshot.ai";
	var resp = goa.http.fetch(base + "/coding/v1/usages", {
		method: "GET",
		headers: { "Authorization": "Bearer " + token },
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
			resetsAt: data.session.reset_at || null
		});
	}
	if (data.weekly) {
		limits.push({
			label: "Weekly",
			used: num(data.weekly.used),
			limit: num(data.weekly.limit),
			resetsAt: data.weekly.reset_at || null
		});
	}
	return { plan: data.plan || null, limits: limits };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "Kimi",
	auth: AUTH,
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
