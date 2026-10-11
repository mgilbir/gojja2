// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

//go:build race

package gojja2_test

// raceDetector reports that the tests run under -race, where sync.Pool drops
// what it is given at random and the detector adds allocations of its own: an
// allocation count measured there is the detector's as much as the engine's.
const raceDetector = true
