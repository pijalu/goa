// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// resolveSession asks the server which session to attach to: a multi-project
// supervisor turns Path into a session; anything else answers /healthz with
// the live session id.
func resolveSession(base string, opts Options) (string, error) {
	if opts.Path != "" {
		if id, err := connectForPath(base, opts); err == nil {
			return id, nil
		}
		// A plain (single-project) server has no /connect: fall through to
		// /healthz, which every server answers.
	}
	req, err := newRequest(base, "/healthz", http.MethodGet, "", opts)
	if err != nil {
		return "", err
	}
	var h struct {
		Session string `json:"session"`
	}
	if err := doJSON(req, &h); err != nil {
		return "", err
	}
	if h.Session == "" {
		return "", fmt.Errorf("server reported no active session; start one or pass --session")
	}
	return h.Session, nil
}

// connectForPath asks a supervisor to open (or reuse) the session for path.
func connectForPath(base string, opts Options) (string, error) {
	body, _ := json.Marshal(map[string]string{"path": opts.Path})
	req, err := newRequest(base, "/connect", http.MethodPost, string(body), opts)
	if err != nil {
		return "", err
	}
	var r struct {
		Session string `json:"session"`
		Error   string `json:"error"`
	}
	if err := doJSON(req, &r); err != nil {
		return "", err
	}
	if r.Session == "" {
		return "", fmt.Errorf("connect: %s", r.Error)
	}
	return r.Session, nil
}

// newRequest builds the plain-HTTP request the attach handshake needs,
// carrying the same credentials the socket will.
func newRequest(base, path, method, body string, opts Options) (*http.Request, error) {
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	opts.addAuthHeader(req)
	return req, nil
}

// doJSON executes req and decodes the JSON body, mapping non-200s to errors.
func doJSON(req *http.Request, out any) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s", req.Method, req.URL.Path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// addAuth puts the credentials on a WebSocket handshake header.
func (o Options) addAuth(hdr http.Header) {
	if o.Token != "" {
		hdr.Set("Authorization", "Bearer "+o.Token)
	} else if o.User != "" || o.Password != "" {
		hdr.Set("Authorization", "Basic "+basicAuth(o.User, o.Password))
	}
}

// addAuthHeader puts the credentials on a plain HTTP request.
func (o Options) addAuthHeader(req *http.Request) {
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	} else if o.User != "" || o.Password != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(o.User, o.Password))
	}
}

// baseURL normalises the --server value to an http(s) base URL.
func baseURL(server string) (string, error) {
	if server == "" {
		return "", fmt.Errorf("no server address given (--server host:port)")
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	u, err := url.Parse(server)
	if err != nil {
		return "", fmt.Errorf("--server: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

// wsScheme swaps the WebSocket scheme for a base URL.
func wsScheme(base string) string {
	if strings.HasPrefix(base, "https://") {
		return "wss://" + strings.TrimPrefix(base, "https://")
	}
	return "ws://" + strings.TrimPrefix(base, "http://")
}

// basicAuth encodes the basic scheme's user:password credentials.
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
