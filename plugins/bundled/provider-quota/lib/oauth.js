// oauth.js — OAuth device-code flow + token lifecycle for quota providers.
//
// Quota auth is separate from model auth: a provider may use an API key for
// inference but OAuth for its quota console (OpenCode, Kimi). This module
// owns the device-code dance and transparent access-token refresh, persisting
// credentials through goa.storage.
//
// Token keys in storage (per provider id):
//   <id>.access_token   — current bearer token
//   <id>.refresh_token  — long-lived refresh credential
//   <id>.expires_at     — ms epoch when access_token expires

var REFRESH_SKEW_MS = 5 * 60 * 1000; // refresh when within 5 min of expiry

// getToken returns a valid access token for providerId, refreshing it when
// near expiry. Returns null when no token is stored or refresh fails (the
// caller surfaces auth_required so the user runs /quota:login:<provider>).
//
// cfg is the fetcher's auth descriptor: { tokenUrl, clientId, scope? }.
function getToken(providerId, cfg) {
	var token = goa.storage.get(providerId + ".access_token");
	var expiresAt = num(goa.storage.get(providerId + ".expires_at"));
	if (!token) {
		return null;
	}
	// Not near expiry → reuse.
	if (!expiresAt || Date.now() < expiresAt - REFRESH_SKEW_MS) {
		return token;
	}
	// Near/past expiry → refresh.
	var refreshed = refreshToken(providerId, cfg);
	return refreshed;
}

// refreshToken exchanges the stored refresh_token for a new access token.
// Returns the new access token, or null on failure (needs re-login).
function refreshToken(providerId, cfg) {
	var refresh = goa.storage.get(providerId + ".refresh_token");
	if (!refresh || !cfg || !cfg.tokenUrl) {
		return null;
	}
	var resp = goa.http.fetch(cfg.tokenUrl, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: {
			grant_type: "refresh_token",
			refresh_token: refresh,
			client_id: cfg.clientId
		}
	});
	if (resp.error || resp.status !== 200) {
		return null;
	}
	var body = parseJSON(resp.body);
	if (!body || !body.access_token) {
		return null;
	}
	storeTokens(providerId, body);
	return body.access_token;
}

// startDeviceFlow begins the device-code dance for providerId. cfg needs
// { authUrl, tokenUrl, clientId, verificationUri? }. It requests a device
// code, prints + opens the verification URL, then polls for completion.
//
// Because goa runs JS synchronously, polling uses goa.setInterval so the
// command returns immediately and completion is reported asynchronously via
// goa.output. onDone(err) is invoked when the flow finishes or fails.
function startDeviceFlow(providerId, cfg, onDone) {
	var codeResp = goa.http.fetch(cfg.authUrl, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: { client_id: cfg.clientId, scope: cfg.scope || "" }
	});
	if (codeResp.error || codeResp.status >= 400) {
		onDone("device code request failed: " + (codeResp.error || ("HTTP " + codeResp.status)));
		return;
	}
	var data = parseJSON(codeResp.body);
	if (!data || !data.device_code) {
		onDone("device code response missing device_code");
		return;
	}

	var verifyUrl = data.verification_uri_complete || data.verification_uri || cfg.verificationUri || cfg.authUrl;
	var userCode = data.user_code || "";
	goa.output("Authorize " + providerId + " quota access:\n  " + verifyUrl +
		(userCode ? "\nEnter code: " + userCode : ""));
	goa.openBrowser(verifyUrl);

	var intervalSec = data.interval || 5;
	var deviceCode = data.device_code;
	pollForToken(providerId, cfg, deviceCode, intervalSec * 1000, onDone);
}

// pollForToken schedules device-token polls until success, denial, or expiry.
function pollForToken(providerId, cfg, deviceCode, intervalMs, onDone) {
	var attempts = 0;
	var maxAttempts = 120; // ~10 min at 5s
	var timerId = goa.setInterval(function() {
		attempts++;
		if (attempts > maxAttempts) {
			goa.clearInterval(timerId);
			onDone("authorization timed out");
			return;
		}
		var resp = goa.http.fetch(cfg.tokenUrl, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: {
				grant_type: "urn:ietf:params:oauth:grant-type:device_code",
				device_code: deviceCode,
				client_id: cfg.clientId
			}
		});
		if (resp.error) {
			return; // transient network error; keep polling
		}
		var body = parseJSON(resp.body);
		if (!body) {
			return;
		}
		if (body.access_token) {
			goa.clearInterval(timerId);
			storeTokens(providerId, body);
			onDone(null);
			return;
		}
		var err = body.error || "";
		if (err === "authorization_pending" || err === "slow_down") {
			return; // keep waiting
		}
		if (err === "access_denied" || err === "expired_token") {
			goa.clearInterval(timerId);
			onDone("authorization " + (err === "access_denied" ? "denied" : "expired"));
			return;
		}
		// Unknown transient error: keep polling until timeout.
	}, Math.max(intervalMs, 1000));
}

// storeTokens persists a token response under the provider's keys.
function storeTokens(providerId, tokenResp) {
	goa.storage.set(providerId + ".access_token", tokenResp.access_token);
	if (tokenResp.refresh_token) {
		goa.storage.set(providerId + ".refresh_token", tokenResp.refresh_token);
	}
	var expiresIn = num(tokenResp.expires_in);
	if (expiresIn > 0) {
		goa.storage.set(providerId + ".expires_at", String(Date.now() + expiresIn * 1000));
	}
}

// logout clears all stored credentials for providerId.
function logout(providerId) {
	goa.storage.delete(providerId + ".access_token");
	goa.storage.delete(providerId + ".refresh_token");
	goa.storage.delete(providerId + ".expires_at");
}

// hasToken reports whether a (possibly expired) token is stored.
function hasToken(providerId) {
	return !!goa.storage.get(providerId + ".access_token");
}

// --- helpers ---

function parseJSON(body) {
	if (!body) {
		return null;
	}
	try {
		return JSON.parse(body);
	} catch (e) {
		return null;
	}
}

function num(v) {
	var n = Number(v);
	return isNaN(n) ? 0 : n;
}

exports.getToken = getToken;
exports.refreshToken = refreshToken;
exports.startDeviceFlow = startDeviceFlow;
exports.logout = logout;
exports.hasToken = hasToken;
exports.REFRESH_SKEW_MS = REFRESH_SKEW_MS;
