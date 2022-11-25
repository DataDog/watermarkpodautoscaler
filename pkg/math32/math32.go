package math32

import "math"

func Max(x, y int32) int32 {
	if x < y {
		return y
	}
	return x
}

func Floor(a float64) int32 {
	return int32(math.Floor(a))
}

func Ceil(a float64) int32 {
	return int32(math.Ceil(a))
}

func Mod(x int32, y int32) int32 {
	return int32(math.Mod(float64(x), float64(y)))
}
