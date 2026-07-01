// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"math"
	"math/rand"
	"time"
)

// RetryWithBackoff executes fn up to maxRetries times with exponential backoff.
// Returns fn's result on success or the last error after all attempts.
//
// Deprecated: This function is not context-aware and uses bare time.Sleep.
// New code should use agent.go's retryStream which respects context
// cancellation.
func RetryWithBackoff(maxRetries int, maxDelay time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDuration(attempt, maxDelay)
			time.Sleep(delay)
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

// backoffDuration computes the sleep duration for a given attempt with jitter.
func backoffDuration(attempt int, maxDelay time.Duration) time.Duration {
	if maxDelay == 0 {
		maxDelay = 30 * time.Second
	}
	base := time.Duration(math.Pow(2, float64(attempt))) * 100 * time.Millisecond
	jitter := time.Duration(rand.Intn(100)) * time.Millisecond
	d := base + jitter
	if d > maxDelay {
		d = maxDelay
	}
	return d
}
