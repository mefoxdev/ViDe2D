package vm

import (
	"math"
	"testing"
)

func TestSinCosAccuracy(t *testing.T) {
	worst := 0.0
	for i := -72000; i <= 72000; i++ { // every 0.01 degree over -720..720
		d := float64(i) / 100
		// compare against math with an exactly reduced angle to keep the reference itself accurate
		ref := math.Sin(math.Mod(d, 360) * math.Pi / 180)
		refc := math.Cos(math.Mod(d, 360) * math.Pi / 180)
		worst = math.Max(worst, math.Abs(SinDeg(d)-ref))
		worst = math.Max(worst, math.Abs(CosDeg(d)-refc))
	}
	if worst > 1e-14 {
		t.Fatalf("worst error %g", worst)
	}
	for _, d := range []float64{0.001, 1e-9, 45, 44.9999999, 89.999, 123.456789, 1e6 + 0.5, 359.99999, -17.3, 1e12 + 0.25} {
		r := math.Mod(d, 360) * math.Pi / 180
		if math.Abs(SinDeg(d)-math.Sin(r)) > 1e-12 || math.Abs(CosDeg(d)-math.Cos(r)) > 1e-12 {
			t.Errorf("%v: sin %v want %v, cos %v want %v", d, SinDeg(d), math.Sin(r), CosDeg(d), math.Cos(r))
		}
	}
}

func TestSinCosExactMultiples(t *testing.T) {
	want := map[float64][2]float64{0: {0, 1}, 90: {1, 0}, 180: {0, -1}, 270: {-1, 0}, 360: {0, 1}, -90: {-1, 0}, -180: {0, -1},
		450: {1, 0}, 3600: {0, 1}, 36000090: {1, 0}, 30: {0.5, math.Cos(30 * math.Pi / 180)}, 150: {0.5, math.Cos(150 * math.Pi / 180)}}
	for d, w := range want {
		s, c := SinDeg(d), CosDeg(d)
		if d == 30 || d == 150 {
			if s != 0.5 {
				t.Errorf("sin %v = %v", d, s)
			}
			continue
		}
		if s != w[0] || c != w[1] {
			t.Errorf("%v: sin %v cos %v, want %v %v", d, s, c, w[0], w[1])
		}
	}
	if CosDeg(60) != 0.5 || CosDeg(-60) != 0.5 || CosDeg(300) != 0.5 {
		t.Error("cos 60")
	}
	if math.Signbit(SinDeg(-180)) || math.Signbit(SinDeg(-0.0)) {
		t.Error("negative zero")
	}
}

func TestSinCosSymmetry(t *testing.T) {
	for i := 0; i < 5000; i++ {
		d := float64(i) * 0.37
		if SinDeg(-d) != -SinDeg(d) || CosDeg(-d) != CosDeg(d) {
			t.Fatalf("parity at %v", d)
		}
		if got, want := SinDeg(d+360), SinDeg(d); math.Abs(got-want) > 1e-12 {
			t.Fatalf("period at %v: %v %v", d, got, want)
		}
		if e := math.Abs(SinDeg(d)*SinDeg(d) + CosDeg(d)*CosDeg(d) - 1); e > 1e-14 {
			t.Fatalf("pythagoras at %v: %g", d, e)
		}
		if math.Abs(SinDeg(d)-CosDeg(90-d)) > 1e-14 {
			t.Fatalf("cofunction at %v", d)
		}
	}
}

func TestSinCosNonFinite(t *testing.T) {
	for _, d := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if !math.IsNaN(SinDeg(d)) || !math.IsNaN(CosDeg(d)) {
			t.Errorf("%v", d)
		}
	}
	if v := SinDeg(1e300); math.IsNaN(v) || math.Abs(v) > 1 {
		t.Errorf("huge: %v", v)
	}
}

// The results are pinned bit for bit: they must be the same on every
// architecture (amd64, arm64 ...), so a change here means the numbers of games changed.
func TestSinCosPinnedBits(t *testing.T) {
	pins := []struct {
		deg      float64
		sin, cos uint64
	}{
		{1, 0x3f91df0b2b89dd1e, 0x3feffec097f5af8a},
		{10, 0x3fc63a1a7e0b738a, 0x3fef838b8c811c17},
		{37, 0x3fe342119455beb6, 0x3fe98e6c0ea27a14},
		{45, 0x3fe6a09e667f3bcc, 0x3fe6a09e667f3bcd},
		{60.5, 0x3febd9f4303e5fc2, 0x3fdf83de1b9b276f},
		{123.456, 0x3feab2a9b25a9ab9, 0xbfe1a43870d371f9},
		{-200, 0x3fd5e3a8748a0bf5, 0xbfee11f642522d1c},
		{1000.001, 0xbfef8385314fccbe, 0x3fc63aaaad3b9713},
	}
	for _, p := range pins {
		if math.Float64bits(SinDeg(p.deg)) != p.sin || math.Float64bits(CosDeg(p.deg)) != p.cos {
			t.Errorf("%v: sin %#x cos %#x", p.deg, math.Float64bits(SinDeg(p.deg)), math.Float64bits(CosDeg(p.deg)))
		}
	}
}

// TestSinCosGoldenBits pins the exact bit patterns of SinDeg and CosDeg. The
// functions use only + - * / on float64 with explicit conversions, so every
// architecture (amd64, arm64 with fused multiply-add) must give these very
// bits: a difference here means games would diverge between machines.
func TestSinCosGoldenBits(t *testing.T) {
	golden := []struct {
		deg      float64
		sin, cos uint64
	}{
		{0, 0x0000000000000000, 0x3ff0000000000000},
		{1, 0x3f91df0b2b89dd1e, 0x3feffec097f5af8a},
		{30, 0x3fe0000000000000, 0x3febb67ae8584cab},
		{45, 0x3fe6a09e667f3bcc, 0x3fe6a09e667f3bcd},
		{60, 0x3febb67ae8584cab, 0x3fe0000000000000},
		{89.5, 0x3fefffb02599c9cd, 0x3f81df37c4954c21},
		{123.456, 0x3feab2a9b25a9ab9, 0xbfe1a43870d371f9},
		{359.999, 0xbef24d1521135b06, 0x3fefffffffeb111d},
		{-17.25, 0xbfd2fa89839b2985, 0x3fee8f85fc8f1553},
		{720.5, 0x3f81df37c4954c21, 0x3fefffb02599c9cd},
	}
	for _, g := range golden {
		if got := math.Float64bits(SinDeg(g.deg)); got != g.sin {
			t.Errorf("SinDeg(%v) = %#016x, want %#016x", g.deg, got, g.sin)
		}
		if got := math.Float64bits(CosDeg(g.deg)); got != g.cos {
			t.Errorf("CosDeg(%v) = %#016x, want %#016x", g.deg, got, g.cos)
		}
	}
}
