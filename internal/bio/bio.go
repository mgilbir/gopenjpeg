// Package bio ports bio.c/bio.h: an individual bit input-output stream over a
// byte buffer, implementing the JPEG 2000 0xFF bit-stuffing rule. On output,
// whenever an emitted byte is 0xFF the next byte carries only 7 data bits; on
// input the symmetric rule is applied. The buffering, stuffing and
// end-of-buffer behaviour reproduce the C code exactly.
package bio

// BIO ports opj_bio_t. In C the buffer is described by three pointers
// (start/bp/end); here it is a byte slice with a position index, so
// NumBytes == pos (start is index 0) and end == len(buf).
type BIO struct {
	buf []byte // the working buffer ([bp, bp+len) in C terms)
	pos int    // current index; NumBytes() == pos

	// reg ports opj_bio_t.buf: the 16-bit register holding the byte being
	// assembled/consumed.
	reg uint32
	// ct ports opj_bio_t.ct: on encode, number of bits free to write in reg;
	// on decode, number of bits available to read.
	ct uint32
}

// NewEncoder ports opj_bio_create followed by opj_bio_init_enc: it prepares a
// BIO to write bits into buf (whose length is the maximum output size).
func NewEncoder(buf []byte) *BIO {
	return &BIO{buf: buf, pos: 0, reg: 0, ct: 8}
}

// NewDecoder ports opj_bio_create followed by opj_bio_init_dec: it prepares a
// BIO to read bits from buf.
func NewDecoder(buf []byte) *BIO {
	return &BIO{buf: buf, pos: 0, reg: 0, ct: 0}
}

// NumBytes ports opj_bio_numbytes: the number of bytes consumed/produced so
// far (bp - start).
func (b *BIO) NumBytes() int {
	return b.pos
}

// byteout ports opj_bio_byteout: emit the high byte of the register, applying
// the 0xFF stuffing rule to set ct. Returns false if the output buffer is
// full (bp >= end), matching the C return value.
func (b *BIO) byteout() bool {
	b.reg = (b.reg << 8) & 0xffff
	if b.reg == 0xff00 {
		b.ct = 7
	} else {
		b.ct = 8
	}
	if b.pos >= len(b.buf) {
		return false
	}
	b.buf[b.pos] = byte(b.reg >> 8)
	b.pos++
	return true
}

// bytein ports opj_bio_bytein: pull the next input byte into the register,
// applying the 0xFF stuffing rule to set ct. Returns false at end of buffer.
func (b *BIO) bytein() bool {
	b.reg = (b.reg << 8) & 0xffff
	if b.reg == 0xff00 {
		b.ct = 7
	} else {
		b.ct = 8
	}
	if b.pos >= len(b.buf) {
		return false
	}
	b.reg |= uint32(b.buf[b.pos])
	b.pos++
	return true
}

// PutBit ports opj_bio_putbit: write a single bit. Like the C code it ignores
// the return value of byteout when the register fills.
func (b *BIO) PutBit(bit uint32) {
	if b.ct == 0 {
		b.byteout()
	}
	b.ct--
	b.reg |= bit << b.ct
}

// getbit ports opj_bio_getbit: read a single bit, refilling via bytein when
// exhausted. Like the C code it ignores bytein's return value; reading past the
// end of the buffer yields bits from the (stuffed) register, matching C.
func (b *BIO) getbit() uint32 {
	if b.ct == 0 {
		b.bytein()
	}
	b.ct--
	return (b.reg >> b.ct) & 1
}

// Write ports opj_bio_write: write the low n bits of v, most significant first.
// n must be in [1,32]; the C code asserts this.
func (b *BIO) Write(v, n uint32) {
	for i := int32(n) - 1; i >= 0; i-- {
		b.PutBit((v >> uint(i)) & 1)
	}
}

// Read ports opj_bio_read: read n bits, most significant first, into the
// returned value. n must be >= 1 (C asserts). For n <= 32 there is no overflow.
func (b *BIO) Read(n uint32) uint32 {
	var v uint32
	for i := int32(n) - 1; i >= 0; i-- {
		v |= b.getbit() << uint(i)
	}
	return v
}

// Flush ports opj_bio_flush: emit the trailing byte(s) produced by the
// stuffing state. Returns false if the output buffer is exhausted.
func (b *BIO) Flush() bool {
	if !b.byteout() {
		return false
	}
	if b.ct == 7 {
		if !b.byteout() {
			return false
		}
	}
	return true
}

// InAlign ports opj_bio_inalign: consume the ending bits produced by a matching
// Flush so the decoder realigns to a byte boundary. Returns false if a needed
// input byte is missing.
func (b *BIO) InAlign() bool {
	if (b.reg & 0xff) == 0xff {
		if !b.bytein() {
			return false
		}
	}
	b.ct = 0
	return true
}
