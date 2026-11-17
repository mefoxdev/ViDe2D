package vm

import "math"

// sin and cos of an angle in degrees, the same on every CPU.
//
// math.Sin and math.Cos are not used: their results may differ in the last
// bits between architectures (assembly versions, fused multiply-add). Here
// the angle is reduced exactly and then a Taylor polynomial is evaluated with
// + - * only, every operation wrapped in float64() so that the compiler may
// not fuse a multiply and an add (arm64 does that, amd64 does not).
//
// Reduction is exact: math.Mod is exact, and the subtractions of 90, 180, 270
// from a number in [90, 360) lose nothing (Sterbenz lemma). The polynomial is
// only used on [0, 45] degrees, where it is accurate to about 1e-16.
// Multiples of 90 degrees (and sin 30, cos 60) give exact results.

const (
	radPerDeg = math.Pi / 180 // an exact constant expression, rounded once when used

	// Taylor coefficients 1/n!, exact constant expressions.
	f3  = 1.0 / 6
	f5  = 1.0 / 120
	f7  = 1.0 / 5040
	f9  = 1.0 / 362880
	f11 = 1.0 / 39916800
	f13 = 1.0 / 6227020800
	f15 = 1.0 / 1307674368000
	f17 = 1.0 / 355687428096000
	f2  = 1.0 / 2
	f4  = 1.0 / 24
	f6  = 1.0 / 720
	f8  = 1.0 / 40320
	f10 = 1.0 / 3628800
	f12 = 1.0 / 479001600
	f14 = 1.0 / 87178291200
	f16 = 1.0 / 20922789888000
)

// sinPoly is sin of t degrees for 0 <= t <= 45.
func sinPoly(t float64) float64 {
	x := float64(t * radPerDeg)
	x2 := float64(x * x)
	p := float64(f17)
	p = float64(float64(p*x2) - f15)
	p = float64(float64(p*x2) + f13)
	p = float64(float64(p*x2) - f11)
	p = float64(float64(p*x2) + f9)
	p = float64(float64(p*x2) - f7)
	p = float64(float64(p*x2) + f5)
	p = float64(float64(p*x2) - f3)
	p = float64(float64(p*x2) + 1)
	return float64(x * p)
}

// cosPoly is cos of t degrees for 0 <= t <= 45.
func cosPoly(t float64) float64 {
	x := float64(t * radPerDeg)
	x2 := float64(x * x)
	p := float64(f16)
	p = float64(float64(p*x2) - f14)
	p = float64(float64(p*x2) + f12)
	p = float64(float64(p*x2) - f10)
	p = float64(float64(p*x2) + f8)
	p = float64(float64(p*x2) - f6)
	p = float64(float64(p*x2) + f4)
	p = float64(float64(p*x2) - f2)
	return float64(float64(p*x2) + 1)
}

// sinQ and cosQ are sin and cos of b degrees for 0 <= b < 90.
func sinQ(b float64) float64 {
	switch {
	case b == 30:
		return 0.5
	case b <= 45:
		return sinPoly(b)
	}
	return cosPoly(float64(90 - b)) // 90 - b is exact for 45 <= b <= 90
}

func cosQ(b float64) float64 {
	switch {
	case b == 60:
		return 0.5
	case b <= 45:
		return cosPoly(b)
	}
	return sinPoly(float64(90 - b))
}

// quadrant splits a >= 0 into a whole number of quarter turns (0..3) and the
// rest b in [0, 90), exactly.
func quadrant(a float64) (q int, b float64) {
	a = math.Mod(a, 360)
	switch {
	case a >= 270:
		return 3, float64(a - 270)
	case a >= 180:
		return 2, float64(a - 180)
	case a >= 90:
		return 1, float64(a - 90)
	}
	return 0, a
}

// SinDeg is the sine of an angle in degrees. NaN and infinities give NaN.
func SinDeg(deg float64) float64 {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return math.NaN()
	}
	neg := deg < 0
	q, b := quadrant(math.Abs(deg))
	var r float64
	switch q {
	case 0:
		r = sinQ(b)
	case 1:
		r = cosQ(b)
	case 2:
		r = float64(-sinQ(b))
	default:
		r = float64(-cosQ(b))
	}
	if neg {
		r = float64(-r)
	}
	if r == 0 {
		return 0 // no negative zero
	}
	return r
}

// CosDeg is the cosine of an angle in degrees. NaN and infinities give NaN.
func CosDeg(deg float64) float64 {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return math.NaN()
	}
	q, b := quadrant(math.Abs(deg)) // cos is even
	var r float64
	switch q {
	case 0:
		r = cosQ(b)
	case 1:
		r = float64(-sinQ(b))
	case 2:
		r = float64(-cosQ(b))
	default:
		r = sinQ(b)
	}
	if r == 0 {
		return 0
	}
	return r
}
