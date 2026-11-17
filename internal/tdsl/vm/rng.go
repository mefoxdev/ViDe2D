package vm

// PCG is the PCG-XSH-RR 64/32 generator. It uses only integer arithmetic, so
// it gives the same sequence on every platform.
type PCG struct {
	state, inc uint64
}

const (
	pcgMult = 6364136223846793005
	pcgSeq  = 54
)

// NewPCG returns a generator seeded with seed (stream 54, as in the reference
// implementation's demo).
func NewPCG(seed uint64) *PCG {
	r := &PCG{inc: pcgSeq<<1 | 1}
	r.Uint32()
	r.state += seed
	r.Uint32()
	return r
}

// Uint32 returns the next 32 random bits.
func (r *PCG) Uint32() uint32 {
	old := r.state
	r.state = old*pcgMult + r.inc
	xorshifted := uint32(((old >> 18) ^ old) >> 27)
	rot := uint32(old >> 59)
	return (xorshifted >> rot) | (xorshifted << ((-rot) & 31))
}

// Uint64 returns the next 64 random bits.
func (r *PCG) Uint64() uint64 {
	hi := uint64(r.Uint32())
	return hi<<32 | uint64(r.Uint32())
}

// Below returns a number in [0, n); n must be > 0. The bias of the modulo is
// below n/2^64 and is ignored on purpose: it keeps the sequence simple to
// reproduce in other implementations.
func (r *PCG) Below(n uint64) uint64 { return r.Uint64() % n }
