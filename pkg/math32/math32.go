package math32

import "math"

// Max returns the maximum of y and x.
func Max(x, y int32) int32 {
	if x < y {
		return y
	}
	return x
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
