package store

import "encoding/binary"

const isaac64Size = 256

type isaac64State struct {
	memory  [isaac64Size]uint64
	results [isaac64Size]uint64
	a       uint64
	b       uint64
	c       uint64
	count   int
}

func newISAAC64(seed uint64) *isaac64State {
	state := &isaac64State{}
	state.results[0] = seed
	state.initialize()
	return state
}

func isaac64Mix(a, b, c, d, e, f, g, h *uint64) {
	*a -= *e
	*f ^= *h >> 9
	*h += *a
	*b -= *f
	*g ^= *a << 9
	*a += *b
	*c -= *g
	*h ^= *b >> 23
	*b += *c
	*d -= *h
	*a ^= *c << 15
	*c += *d
	*e -= *a
	*b ^= *d >> 14
	*d += *e
	*f -= *b
	*c ^= *e << 20
	*e += *f
	*g -= *c
	*d ^= *f >> 17
	*f += *g
	*h -= *d
	*e ^= *g << 14
	*g += *h
}

func (state *isaac64State) initialize() {
	state.a, state.b, state.c = 0, 0, 0
	a := uint64(0x9e3779b97f4a7c13)
	b, c, d, e, f, g, h := a, a, a, a, a, a, a
	for index := 0; index < 4; index++ {
		isaac64Mix(&a, &b, &c, &d, &e, &f, &g, &h)
	}
	for index := 0; index < isaac64Size; index += 8 {
		a += state.results[index]
		b += state.results[index+1]
		c += state.results[index+2]
		d += state.results[index+3]
		e += state.results[index+4]
		f += state.results[index+5]
		g += state.results[index+6]
		h += state.results[index+7]
		isaac64Mix(&a, &b, &c, &d, &e, &f, &g, &h)
		state.memory[index] = a
		state.memory[index+1] = b
		state.memory[index+2] = c
		state.memory[index+3] = d
		state.memory[index+4] = e
		state.memory[index+5] = f
		state.memory[index+6] = g
		state.memory[index+7] = h
	}
	for index := 0; index < isaac64Size; index += 8 {
		a += state.memory[index]
		b += state.memory[index+1]
		c += state.memory[index+2]
		d += state.memory[index+3]
		e += state.memory[index+4]
		f += state.memory[index+5]
		g += state.memory[index+6]
		h += state.memory[index+7]
		isaac64Mix(&a, &b, &c, &d, &e, &f, &g, &h)
		state.memory[index] = a
		state.memory[index+1] = b
		state.memory[index+2] = c
		state.memory[index+3] = d
		state.memory[index+4] = e
		state.memory[index+5] = f
		state.memory[index+6] = g
		state.memory[index+7] = h
	}
	state.generate()
	state.count = isaac64Size
}

func (state *isaac64State) generate() {
	state.c++
	state.b += state.c
	for index := 0; index < isaac64Size; index++ {
		x := state.memory[index]
		switch index & 3 {
		case 0:
			state.a = ^(state.a ^ (state.a << 21))
		case 1:
			state.a ^= state.a >> 5
		case 2:
			state.a ^= state.a << 12
		case 3:
			state.a ^= state.a >> 33
		}
		state.a += state.memory[(index+128)&255]
		y := state.memory[(x>>3)&255] + state.a + state.b
		state.memory[index] = y
		state.b = state.memory[(y>>11)&255] + x
		state.results[index] = state.b
	}
}

func (state *isaac64State) next() uint64 {
	if state.count == 0 {
		state.generate()
		state.count = isaac64Size
	}
	state.count--
	return state.results[state.count]
}

func isaac64Keystream(seed uint64, size int) []byte {
	stream := make([]byte, size)
	state := newISAAC64(seed)
	for offset := 0; offset < size; offset += 8 {
		var block [8]byte
		binary.BigEndian.PutUint64(block[:], state.next())
		copy(stream[offset:], block[:])
	}
	return stream
}
