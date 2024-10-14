// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package math32 package contains utils to manipulate the different types.
package math32

import "math"

// Max returns the maximum of y and x.
func Max(x, y int32) int32 {
	if x < y {
		return y
	}
	return x
}

// Min returns minimum of x and x.
func Min(x, y int32) int32 {
	if x > y {
		return y
	}
	return x
}

// Cap returns the value of x capped to the range [a, b].
func Cap(x, a, b int32) int32 {
	return Max(a, Min(x, b))
}

// Floor returns the greatest integer value less than or equal to x.
func Floor(a float64) int32 {
	return int32(math.Floor(a))
}

// Ceil returns the least integer value greater than or equal to x.
func Ceil(a float64) int32 {
	return int32(math.Ceil(a))
}

// Mod returns the floating-point remainder of x/y.
func Mod(x int32, y int32) int32 {
	return int32(math.Mod(float64(x), float64(y)))
}
