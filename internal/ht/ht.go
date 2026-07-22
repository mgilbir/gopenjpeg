package ht

// This file is a faithful port of OpenJPEG's ht_dec.c: the High-Throughput
// JPEG 2000 (HTJ2K, Rec. ITU-T T.814 / ISO/IEC 15444-15) code-block decoder.
//
// The decoder consumes a single HT code-block (one MagSgn+MEL+VLC "cleanup"
// pass, optionally followed by a SigProp pass and a MagRef pass) and produces
// the reconstructed coefficient raster (int32, natural raster order, sign +
// magnitude before ROI de-scaling / dequantization) — the same output
// convention as internal/t1's DecodeCblk, so that tcd can treat both uniformly.
//
// C integer widths are replicated exactly (the bit readers use 64-bit
// accumulators, as OPJ_UINT64 in the reference). Every malformed-stream guard
// in ht_dec.c is preserved, reporting through *event.Manager with the same
// warning-vs-error outcomes.

import (
	"sync"
)

// onlyCleanupPassWarned mirrors the C file-static only_cleanup_pass_is_decoded:
// the "only the cleanup pass will be decoded" warning is emitted at most once
// across the lifetime of the process.
var onlyCleanupPassWarned sync.Once

// b2u returns 1 for true and 0 for false (the C idiom of adding an OPJ_BOOL).
func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// b2i is b2u for signed accumulators.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// at reads one byte at index pos, returning 0 for any out-of-range index so
// that malformed/fuzz inputs never trigger a runtime bounds-check panic. For
// valid streams the callers' size bookkeeping keeps pos in range, so this
// matches the C pointer reads exactly.
func at(b []byte, pos int) uint32 {
	if pos >= 0 && pos < len(b) {
		return uint32(b[pos])
	}
	return 0
}

// leU32 ports read_le_uint32: read a little-endian uint32 at index pos. Unlike
// the C code it never reads out of bounds — for valid streams the callers'
// size bookkeeping guarantees pos and pos+4 are in range, and the fallback only
// matters for deliberately malformed fuzz inputs (where it zero-fills).
func leU32(b []byte, pos int) uint32 {
	if pos >= 0 && pos+4 <= len(b) {
		return uint32(b[pos]) | uint32(b[pos+1])<<8 |
			uint32(b[pos+2])<<16 | uint32(b[pos+3])<<24
	}
	var v uint32
	for i := 0; i < 4; i++ {
		p := pos + i
		if p >= 0 && p < len(b) {
			v |= uint32(b[p]) << (8 * uint(i))
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// MEL decoder (dec_mel_t)
// ---------------------------------------------------------------------------

// decMel ports dec_mel_t: reads and decodes the MEL sub-bitstream into runs.
type decMel struct {
	buf     []byte
	pos     int    // index of next byte (data pointer)
	tmp     uint64 // temporary buffer of read data
	bits    int    // number of bits stored in tmp
	size    int    // number of bytes left in MEL code
	unstuff bool   // true if the next bit needs unstuffing
	k       int    // state of MEL decoder
	numRuns int    // number of decoded runs in runs (max 8)
	runs    uint64 // queue of decoded runs (7 bits/run)
}

// read ports mel_read: reads and unstuffs up to 32 bits from the MEL bitstream.
func (m *decMel) read() {
	if m.bits > 32 { // enough bits already
		return
	}

	val := uint32(0xFFFFFFFF) // feed 0xFF if buffer exhausted
	if m.size > 4 {
		val = leU32(m.buf, m.pos)
		m.pos += 4
		m.size -= 4
	} else if m.size > 0 {
		i := 0
		for m.size > 1 {
			v := at(m.buf, m.pos)
			m.pos++
			mask := ^(uint32(0xFF) << uint(i))
			val = (val & mask) | (v << uint(i))
			m.size--
			i += 8
		}
		// size == 1: the one before the last is different
		v := at(m.buf, m.pos)
		m.pos++
		v |= 0xF // MEL and VLC segments can overlap
		mask := ^(uint32(0xFF) << uint(i))
		val = (val & mask) | (v << uint(i))
		m.size--
	}

	bitsN := 32 - b2i(m.unstuff)

	t := val & 0xFF
	u := b2i((val & 0xFF) == 0xFF)
	bitsN -= u
	t = t << uint(8-u)

	t |= (val >> 8) & 0xFF
	u = b2i(((val >> 8) & 0xFF) == 0xFF)
	bitsN -= u
	t = t << uint(8-u)

	t |= (val >> 16) & 0xFF
	u = b2i(((val >> 16) & 0xFF) == 0xFF)
	bitsN -= u
	t = t << uint(8-u)

	t |= (val >> 24) & 0xFF
	m.unstuff = ((val >> 24) & 0xFF) == 0xFF

	m.tmp |= uint64(t) << uint(64-bitsN-m.bits)
	m.bits += bitsN
}

// melExp are the MEL exponents indexed by state k.
var melExp = [13]int{0, 0, 0, 1, 1, 1, 2, 2, 2, 3, 3, 4, 5}

// decode ports mel_decode: decode unstuffed MEL bits in tmp into runs.
func (m *decMel) decode() {
	if m.bits < 6 {
		m.read()
	}
	for m.bits >= 6 && m.numRuns < 8 {
		eval := melExp[m.k]
		var run int
		if m.tmp&(1<<63) != 0 { // next bit is 1
			run = 1 << uint(eval)
			run--
			if m.k+1 < 12 {
				m.k++
			} else {
				m.k = 12
			}
			m.tmp <<= 1
			m.bits--
			run <<= 1
		} else { // next bit is 0
			run = int((m.tmp >> uint(63-eval)) & ((1 << uint(eval)) - 1))
			if m.k-1 > 0 {
				m.k--
			} else {
				m.k = 0
			}
			m.tmp <<= uint(eval + 1)
			m.bits -= eval + 1
			run = (run << 1) + 1
		}
		e := m.numRuns * 7
		m.runs &= ^(uint64(0x3F) << uint(e))
		m.runs |= uint64(run) << uint(e)
		m.numRuns++
	}
}

// init ports mel_init. Returns false on the "Incorrect MEL segment sequence"
// malformed condition (mel_init returning OPJ_FALSE).
func (m *decMel) init(buf []byte, lcup, scup int) bool {
	m.buf = buf
	m.pos = lcup - scup
	m.bits = 0
	m.tmp = 0
	m.unstuff = false
	m.size = scup - 1
	m.k = 0
	m.numRuns = 0
	m.runs = 0

	num := 4 - (m.pos & 3)
	for i := 0; i < num; i++ {
		if m.unstuff {
			var cur byte
			if m.pos >= 0 && m.pos < len(buf) {
				cur = buf[m.pos]
			}
			if cur > 0x8F {
				return false
			}
		}
		var d uint32
		if m.size > 0 {
			d = at(buf, m.pos)
		} else {
			d = 0xFF
		}
		if m.size == 1 {
			d |= 0xF
		}
		if m.size > 0 {
			m.pos++
		}
		m.size--
		dbits := 8 - b2i(m.unstuff)
		m.tmp = (m.tmp << uint(dbits)) | uint64(d)
		m.bits += dbits
		m.unstuff = (d & 0xFF) == 0xFF
	}
	m.tmp <<= uint(64 - m.bits)
	return true
}

// getRun ports mel_get_run.
func (m *decMel) getRun() int {
	if m.numRuns == 0 {
		m.decode()
	}
	t := int(m.runs & 0x7F)
	m.runs >>= 7
	m.numRuns--
	return t
}

// ---------------------------------------------------------------------------
// Reverse-growing bit reader (rev_struct_t) — VLC and MagRef
// ---------------------------------------------------------------------------

// revStruct ports rev_struct_t: a segment that grows backward.
type revStruct struct {
	buf     []byte
	pos     int // index of next byte to read (moving backward)
	tmp     uint64
	bits    uint32
	size    int
	unstuff bool
}

// read ports rev_read (identical to rev_read_mrp; both fill zeros when the
// available data is consumed).
func (v *revStruct) read() {
	if v.bits > 32 {
		return
	}
	var val uint32
	if v.size > 3 {
		val = leU32(v.buf, v.pos-3)
		v.pos -= 4
		v.size -= 4
	} else if v.size > 0 {
		i := 24
		for v.size > 0 {
			vv := uint32(0)
			if v.pos >= 0 && v.pos < len(v.buf) {
				vv = uint32(v.buf[v.pos])
			}
			v.pos--
			val |= vv << uint(i)
			v.size--
			i -= 8
		}
	}

	tmp := val >> 24
	bitsN := 8 - b2u(v.unstuff && (((val>>24)&0x7F) == 0x7F))
	unstuff := (val >> 24) > 0x8F

	tmp |= ((val >> 16) & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff && (((val>>16)&0x7F) == 0x7F))
	unstuff = ((val >> 16) & 0xFF) > 0x8F

	tmp |= ((val >> 8) & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff && (((val>>8)&0x7F) == 0x7F))
	unstuff = ((val >> 8) & 0xFF) > 0x8F

	tmp |= (val & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff && ((val&0x7F) == 0x7F))
	unstuff = (val & 0xFF) > 0x8F

	v.tmp |= uint64(tmp) << v.bits
	v.bits += bitsN
	v.unstuff = unstuff
}

// initVLC ports rev_init.
func (v *revStruct) initVLC(buf []byte, lcup, scup int) {
	v.buf = buf
	v.pos = lcup - 2
	v.size = scup - 2

	d := uint32(0)
	if v.pos >= 0 && v.pos < len(buf) {
		d = uint32(buf[v.pos])
	}
	v.pos--
	v.tmp = uint64(d >> 4)
	v.bits = 4 - b2u((v.tmp&7) == 7)
	v.unstuff = (d | 0xF) > 0x8F

	num := 1 + (v.pos & 3)
	tnum := num
	if v.size < tnum {
		tnum = v.size
	}
	for i := 0; i < tnum; i++ {
		dd := uint32(0)
		if v.pos >= 0 && v.pos < len(buf) {
			dd = uint32(buf[v.pos])
		}
		v.pos--
		dbits := 8 - b2u(v.unstuff && ((dd&0x7F) == 0x7F))
		v.tmp |= uint64(dd) << v.bits
		v.bits += dbits
		v.unstuff = dd > 0x8F
	}
	v.size -= tnum
	v.read()
}

// initMRP ports rev_init_mrp.
func (v *revStruct) initMRP(buf []byte, lcup, len2 int) {
	v.buf = buf
	v.pos = lcup + len2 - 1
	v.size = len2
	v.unstuff = true
	v.bits = 0
	v.tmp = 0

	num := 1 + (v.pos & 3)
	for i := 0; i < num; i++ {
		var d uint32
		if v.size > 0 {
			if v.pos >= 0 && v.pos < len(buf) {
				d = uint32(buf[v.pos])
			}
			v.pos--
		}
		v.size--
		dbits := 8 - b2u(v.unstuff && ((d&0x7F) == 0x7F))
		v.tmp |= uint64(d) << v.bits
		v.bits += dbits
		v.unstuff = d > 0x8F
	}
	v.read()
}

// fetch ports rev_fetch / rev_fetch_mrp.
func (v *revStruct) fetch() uint32 {
	if v.bits < 32 {
		v.read()
		if v.bits < 32 {
			v.read()
		}
	}
	return uint32(v.tmp)
}

// advance ports rev_advance / rev_advance_mrp.
func (v *revStruct) advance(numBits uint32) uint32 {
	v.tmp >>= numBits
	v.bits -= numBits
	return uint32(v.tmp)
}

// ---------------------------------------------------------------------------
// Forward-growing bit reader (frwd_struct_t) — MagSgn and SigProp
// ---------------------------------------------------------------------------

// frwdStruct ports frwd_struct_t.
type frwdStruct struct {
	buf     []byte
	pos     int
	tmp     uint64
	bits    uint32
	unstuff bool
	size    int
	X       uint32 // 0 or 0xFF, inserted when the bitstream is exhausted
}

// read ports frwd_read.
func (f *frwdStruct) read() {
	var val uint32
	if f.size > 3 {
		val = leU32(f.buf, f.pos)
		f.pos += 4
		f.size -= 4
	} else if f.size > 0 {
		i := 0
		if f.X != 0 {
			val = 0xFFFFFFFF
		}
		for f.size > 0 {
			v := at(f.buf, f.pos)
			f.pos++
			mask := ^(uint32(0xFF) << uint(i))
			val = (val & mask) | (v << uint(i))
			f.size--
			i += 8
		}
	} else {
		if f.X != 0 {
			val = 0xFFFFFFFF
		}
	}

	bitsN := 8 - b2u(f.unstuff)
	t := val & 0xFF
	unstuff := (val & 0xFF) == 0xFF

	t |= ((val >> 8) & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff)
	unstuff = ((val >> 8) & 0xFF) == 0xFF

	t |= ((val >> 16) & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff)
	unstuff = ((val >> 16) & 0xFF) == 0xFF

	t |= ((val >> 24) & 0xFF) << bitsN
	bitsN += 8 - b2u(unstuff)
	f.unstuff = ((val >> 24) & 0xFF) == 0xFF

	f.tmp |= uint64(t) << f.bits
	f.bits += bitsN
}

// init ports frwd_init. pos is the start index into buf, size the segment
// length, X the exhaustion fill value (0 or 0xFF).
func (f *frwdStruct) init(buf []byte, pos, size int, X uint32) {
	f.buf = buf
	f.pos = pos
	f.tmp = 0
	f.bits = 0
	f.unstuff = false
	f.size = size
	f.X = X

	num := 4 - (pos & 3)
	for i := 0; i < num; i++ {
		var d uint32
		if f.size > 0 {
			d = at(buf, f.pos)
			f.pos++
		} else {
			d = f.X
		}
		f.size--
		f.tmp |= uint64(d) << f.bits
		f.bits += 8 - b2u(f.unstuff)
		f.unstuff = (d & 0xFF) == 0xFF
	}
	f.read()
}

// fetch ports frwd_fetch.
func (f *frwdStruct) fetch() uint32 {
	if f.bits < 32 {
		f.read()
		if f.bits < 32 {
			f.read()
		}
	}
	return uint32(f.tmp)
}

// advance ports frwd_advance.
func (f *frwdStruct) advance(numBits uint32) {
	f.tmp >>= numBits
	f.bits -= numBits
}

// ---------------------------------------------------------------------------
// UVLC decoding
// ---------------------------------------------------------------------------

// uvlcDec is the shared prefix/suffix table for decode_init_uvlc /
// decode_noninit_uvlc: 2 low bits = prefix length, next 3 = suffix length,
// top 3 = prefix value.
var uvlcDec = [8]uint8{
	3 | (5 << 2) | (5 << 5),
	1 | (0 << 2) | (1 << 5),
	2 | (0 << 2) | (2 << 5),
	1 | (0 << 2) | (1 << 5),
	3 | (1 << 2) | (3 << 5),
	1 | (0 << 2) | (1 << 5),
	2 | (0 << 2) | (2 << 5),
	1 | (0 << 2) | (1 << 5),
}

// decodeInitUVLC ports decode_init_uvlc (initial line). Returns consumed bits;
// u[0], u[1] receive u_q + 1 (a partial u + kappa).
func decodeInitUVLC(vlc, mode uint32, u *[2]uint32) uint32 {
	consumed := uint32(0)
	if mode == 0 {
		u[0], u[1] = 1, 1
	} else if mode <= 2 {
		d := uint32(uvlcDec[vlc&0x7])
		vlc >>= d & 0x3
		consumed += d & 0x3
		suffixLen := (d >> 2) & 0x7
		consumed += suffixLen
		d = (d >> 5) + (vlc & ((1 << suffixLen) - 1))
		if mode == 1 {
			u[0] = d + 1
			u[1] = 1
		} else {
			u[0] = 1
			u[1] = d + 1
		}
	} else if mode == 3 {
		d1 := uint32(uvlcDec[vlc&0x7])
		vlc >>= d1 & 0x3
		consumed += d1 & 0x3
		if (d1 & 0x3) > 2 {
			u[1] = (vlc & 1) + 1 + 1
			consumed++
			vlc >>= 1
			suffixLen := (d1 >> 2) & 0x7
			consumed += suffixLen
			d1 = (d1 >> 5) + (vlc & ((1 << suffixLen) - 1))
			u[0] = d1 + 1
		} else {
			d2 := uint32(uvlcDec[vlc&0x7])
			vlc >>= d2 & 0x3
			consumed += d2 & 0x3
			suffixLen := (d1 >> 2) & 0x7
			consumed += suffixLen
			d1 = (d1 >> 5) + (vlc & ((1 << suffixLen) - 1))
			u[0] = d1 + 1
			vlc >>= suffixLen
			suffixLen = (d2 >> 2) & 0x7
			consumed += suffixLen
			d2 = (d2 >> 5) + (vlc & ((1 << suffixLen) - 1))
			u[1] = d2 + 1
		}
	} else if mode == 4 {
		d1 := uint32(uvlcDec[vlc&0x7])
		vlc >>= d1 & 0x3
		consumed += d1 & 0x3
		d2 := uint32(uvlcDec[vlc&0x7])
		vlc >>= d2 & 0x3
		consumed += d2 & 0x3
		suffixLen := (d1 >> 2) & 0x7
		consumed += suffixLen
		d1 = (d1 >> 5) + (vlc & ((1 << suffixLen) - 1))
		u[0] = d1 + 3
		vlc >>= suffixLen
		suffixLen = (d2 >> 2) & 0x7
		consumed += suffixLen
		d2 = (d2 >> 5) + (vlc & ((1 << suffixLen) - 1))
		u[1] = d2 + 3
	}
	return consumed
}

// decodeNoninitUVLC ports decode_noninit_uvlc (non-initial line).
func decodeNoninitUVLC(vlc, mode uint32, u *[2]uint32) uint32 {
	consumed := uint32(0)
	if mode == 0 {
		u[0], u[1] = 1, 1
	} else if mode <= 2 {
		d := uint32(uvlcDec[vlc&0x7])
		vlc >>= d & 0x3
		consumed += d & 0x3
		suffixLen := (d >> 2) & 0x7
		consumed += suffixLen
		d = (d >> 5) + (vlc & ((1 << suffixLen) - 1))
		if mode == 1 {
			u[0] = d + 1
			u[1] = 1
		} else {
			u[0] = 1
			u[1] = d + 1
		}
	} else if mode == 3 {
		d1 := uint32(uvlcDec[vlc&0x7])
		vlc >>= d1 & 0x3
		consumed += d1 & 0x3
		d2 := uint32(uvlcDec[vlc&0x7])
		vlc >>= d2 & 0x3
		consumed += d2 & 0x3
		suffixLen := (d1 >> 2) & 0x7
		consumed += suffixLen
		d1 = (d1 >> 5) + (vlc & ((1 << suffixLen) - 1))
		u[0] = d1 + 1
		vlc >>= suffixLen
		suffixLen = (d2 >> 2) & 0x7
		consumed += suffixLen
		d2 = (d2 >> 5) + (vlc & ((1 << suffixLen) - 1))
		u[1] = d2 + 1
	}
	return consumed
}
