// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// restClientTool implements agentic.Tool for making REST API calls.
type restClientTool struct {
	logger *agentic.Logger
	client *http.Client
}

// NewRestClientTool creates a new restClientTool with default http.Client.
func NewRestClientTool(logger *agentic.Logger) agentic.Tool {
	return &restClientTool{
		logger: logger,
		client: &http.Client{},
	}
}

// NewRestClientToolWithClient creates a new restClientTool with a custom http.Client.
func NewRestClientToolWithClient(client *http.Client, logger *agentic.Logger) agentic.Tool {
	return &restClientTool{
		logger: logger,
		client: client,
	}
}

func (t *restClientTool) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Retry on network-level errors (timeouts, connection failures)
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	if strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "i/o timeout") {
		return true
	}
	return false
}

func (t *restClientTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "rest_api",
		Description: "Make HTTP requests to interact with REST APIs. Supports GET, POST, PUT, DELETE methods.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method": map[string]interface{}{
					"type":        "string",
					"description": "HTTP method to use",
					"enum":        []string{"GET", "POST", "PUT", "DELETE"},
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "Full URL to request (including protocol)",
				},
				"headers": map[string]interface{}{
					"type":        "object",
					"description": "HTTP headers to include (key-value pairs)",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
				"body": map[string]interface{}{
					"type":        "object",
					"description": "Request body as JSON object (for POST/PUT)",
				},
				"query_params": map[string]interface{}{
					"type":        "object",
					"description": "Query parameters to append to URL",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"method", "url"},
		},
	}
}

type restParams struct {
	Method      string                 `json:"method"`
	URL         string                 `json:"url"`
	Headers     map[string]string      `json:"headers"`
	Body        map[string]interface{} `json:"body"`
	QueryParams map[string]string      `json:"query_params"`
}

func (t *restClientTool) Execute(input string) (string, error) {
	params, err := t.parseInput(input)
	if err != nil {
		return "", err
	}

	reqURL, err := buildRequestURL(params.URL, params.QueryParams)
	if err != nil {
		return "", err
	}

	req, err := t.buildRequest(params, reqURL)
	if err != nil {
		return "", err
	}

	resp, err := t.doRequest(req, reqURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	result := formatResponse(resp, respBody)
	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}
	return result, nil
}

func (t *restClientTool) parseInput(input string) (restParams, error) {
	var params restParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return restParams{}, fmt.Errorf("parse input: %w", err)
	}
	if params.URL == "" {
		return restParams{}, fmt.Errorf("url is required")
	}
	return params, nil
}

func buildRequestURL(rawURL string, queryParams map[string]string) (string, error) {
	if len(queryParams) == 0 {
		return rawURL, nil
	}
	values := url.Values{}
	for k, v := range queryParams {
		values.Set(k, v)
	}
	if strings.Contains(rawURL, "?") {
		return rawURL + "&" + values.Encode(), nil
	}
	return rawURL + "?" + values.Encode(), nil
}

func (t *restClientTool) buildRequest(params restParams, reqURL string) (*http.Request, error) {
	bodyReader, err := buildRequestBody(params.Method, params.Body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(params.Method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	setDefaultRestHeaders(req)
	for k, v := range params.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func buildRequestBody(method string, body map[string]interface{}) (io.Reader, error) {
	if body == nil || (method != "POST" && method != "PUT") {
		return nil, nil
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	return bytes.NewReader(bodyBytes), nil
}

func setDefaultRestHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "AgenticBot/1.0 (https://github.com/pijalu/goa/internal/agentic; contact@example.com)")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (t *restClientTool) doRequest(req *http.Request, reqURL string) (*http.Response, error) {
	if t.logger != nil {
		t.logger.Log(agentic.Debug, "REST API: %s %s", req.Method, reqURL)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	return resp, nil
}

func formatResponse(resp *http.Response, respBody []byte) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("HTTP %d %s\n", resp.StatusCode, resp.Status))
	result.WriteString("\n--- Response Headers ---\n")
	for k := range resp.Header {
		result.WriteString(fmt.Sprintf("%s: %s\n", k, resp.Header.Get(k)))
	}
	result.WriteString("\n--- Response Body ---\n")
	result.WriteString(formatResponseBody(respBody))
	return result.String()
}

func formatResponseBody(respBody []byte) string {
	var jsonBody interface{}
	if err := json.Unmarshal(respBody, &jsonBody); err != nil {
		return string(respBody)
	}
	prettyJSON, err := json.MarshalIndent(jsonBody, "", "  ")
	if err != nil {
		return string(respBody)
	}
	return string(prettyJSON)
}
