// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package models provides the built-in model registry.
//
// The hardcoded models in models.go serve as the fallback cache.
// To refresh from models.dev, run:
//
//	go generate ./internal/agentic/provider/models/
//
// This fetches https://models.dev/api.json, parses supported providers,
// and writes models_generated.go. If the remote fetch fails, the
// existing generated file is preserved as cache.
//
//go:generate go run ../../../../cmd/genmodels/main.go
package models
