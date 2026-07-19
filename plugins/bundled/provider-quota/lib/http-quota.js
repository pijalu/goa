// lib/http-quota.js — generic HTTP quota-fetch engine.
//
// Every provider fetcher follows the same skeleton: resolve auth → GET a
// quota endpoint → map transport/status/parse failures onto the shared error
// vocabulary → shape the response into {plan, limits, costUnit?}. This module
// owns the skeleton once; fetchers supply only the provider-specific
// descriptor ({auth, request, map}) so new providers are extensions, not
// re-implementations (Open/Closed).
//
// Error vocabulary (consumed by plugin.js and the status segment):
//   no_api_key     — provider configured without an API key
//   auth_required  — 401/403, or OAuth token missing/expired
//   http_<status>  — any other non-200 response
//   bad_response   — transport error or unparseable body
//   (fetcher-specific errors pass through map() untouched)
//
// Descriptor shape:
// {
//   auth:     (ctx) => string|null          // bearer/api key, null → caller's authError
//   authError: "no_api_key"|"auth_required" // error when auth() is null
//   url:      (ctx) => string               // full request URL
//   headers:  (ctx, token) => object        // request headers
//   extraHeaders: (ctx) => object           // optional static extras (e.g. x-org-id)
//   map:      (body, ctx) => result         // {plan, limits, costUnit?} | {error}
// }
//
// ctx is the fetcher ctx ({config, session}); body is the parsed JSON.

// apiKeyAuth returns an auth resolver for API-key providers: the key comes
// from the provider config, and its absence maps to no_api_key.
function apiKeyAuth() {
	return {
		auth: function(ctx) { return (ctx.config && ctx.config.apiKey) || null; },
		authError: "no_api_key"
	};
}

// oauthTokenAuth returns an auth resolver for OAuth providers: the access
// token comes from oauth.getToken (refresh handled there), and its absence
// maps to auth_required. cfg is the provider's auth descriptor (tokenUrl…).
function oauthTokenAuth(oauth, providerId, cfg) {
	return {
		auth: function() { return oauth.getToken(providerId, cfg); },
		authError: "auth_required"
	};
}

// bearerHeaders is the common Authorization: Bearer header factory.
function bearerHeaders(ctx, token) {
	return { "Authorization": "Bearer " + token, "Accept": "application/json" };
}

// runFetch executes the shared fetch pipeline for a descriptor.
//   {auth, authError, url, headers, extraHeaders?, map}
function runFetch(desc, ctx) {
	var token = desc.auth(ctx);
	if (!token) {
		return { error: desc.authError, plan: null, limits: [] };
	}
	var headers = desc.headers(ctx, token);
	if (desc.extraHeaders) {
		var extra = desc.extraHeaders(ctx);
		for (var k in extra) {
			headers[k] = extra[k];
		}
	}
	return getJSON(desc.url(ctx), headers, function(body) {
		return desc.map(body, ctx);
	});
}

// getJSON performs the GET and funnels transport/status/parse failures onto
// the shared error vocabulary, calling onBody(parsed) on success.
function getJSON(url, headers, onBody) {
	var resp = goa.http.fetch(url, { method: "GET", headers: headers, timeoutMs: 15000 });
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
	return onBody(body);
}

// --- shared mappers ---------------------------------------------------------

// windowedUsageMapper maps the common {session, weekly, ...limits} shape used
// by anthropic/zai/minimax onto window descriptors. spec is
//   { session: {label, periodMs}, weekly: {...}, [extraKey]: {...} }
// and the mapper reads body[path] (default body.usage || body.data || body).
function windowedUsageMapper(spec, path) {
	return function(body) {
		var data = pickRoot(body, path);
		var limits = [];
		for (var key in spec) {
			var w = data[key];
			if (!w) {
				continue;
			}
			limits.push({
				label: spec[key].label,
				used: num(w.used),
				limit: num(w.limit),
				resetsAt: w.reset_at || w.resets_at || null,
				periodMs: spec[key].periodMs
			});
		}
		var plan = (body.plan && body.plan.name) || body.plan || data.plan || null;
		return { plan: plan, limits: limits };
	};
}

// pickRoot unwraps nested response envelopes: body[path], body.usage,
// body.data, or body itself.
function pickRoot(body, path) {
	if (path && body[path]) {
		return body[path];
	}
	return body.usage || body.data || body;
}

// --- helpers ----------------------------------------------------------------

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

exports.apiKeyAuth = apiKeyAuth;
exports.oauthTokenAuth = oauthTokenAuth;
exports.bearerHeaders = bearerHeaders;
exports.runFetch = runFetch;
exports.getJSON = getJSON;
exports.windowedUsageMapper = windowedUsageMapper;
exports.num = num;
exports.parseJSON = parseJSON;
