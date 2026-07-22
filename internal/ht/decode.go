package ht

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/t1"
)

// fail emits the formatted message through the event manager (matching the
// opj_event_msg(EVT_ERROR) call at the corresponding C site) and returns it as
// an error so the failure bubbles up to the caller. Per the project no-panic
// rule, every malformed-stream condition is surfaced as an error.
func fail(em *event.Manager, format string, args ...any) (bool, error) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	em.Errorf("%s", msg)
	return false, errors.New(msg)
}

// failErr is fail for call sites that only propagate an error (no bool).
func failErr(em *event.Manager, format string, args ...any) error {
	_, err := fail(em, format, args...)
	return err
}

// Decoder holds the reusable scratch state for HT code-block decoding, mirroring
// the per-thread reuse of opj_t1_t buffers in the C reference. A single Decoder
// can decode many code-blocks; its buffers grow on demand and are reused to keep
// per-code-block decoding allocation-free.
type Decoder struct {
	// data is the working sample buffer (t1->data, reinterpreted as OPJ_UINT32
	// during decode): sign in bit 31, magnitude below. Length w*h.
	data []uint32
	// result is the signed output (natural raster order) produced by the final
	// sign-magnitude conversion; Data() returns it. When cblk.DecodedData is
	// non-nil the conversion targets that buffer instead.
	result []int32
	// out is the internally-owned output buffer used when DecodedData is nil.
	out []int32

	// Significance / membership scratch (the t1->flags region in C). Each
	// element packs 4 rows x 8 columns of significance nibbles; 132 entries per
	// array (128 needed for 1024 columns, +convenience).
	sigma1, sigma2, mbr1, mbr2 []uint32
	// lineState is the per-quad line-state (the C line_state byte array): MSB is
	// (sigma^nw | sigma^n), low 7 bits max(E^nw | E^n). 528 bytes.
	lineState []uint8

	// cblkBuf concatenates the code-block chunks (t1->cblkdatabuffer).
	cblkBuf []byte

	w, h uint32
}

// New creates an HT code-block decoder (analogous to opj_t1_create for the HT
// path). Buffers are allocated lazily on first use.
func New() *Decoder { return &Decoder{} }

// Data returns the reconstructed coefficient raster for the most recently
// decoded code-block (t1->data, raster order, sign+magnitude int32 before ROI
// de-scaling / dequantization).
func (d *Decoder) Data() []int32 { return d.result }

// W returns the width of the current code-block.
func (d *Decoder) W() uint32 { return d.w }

// H returns the height of the current code-block.
func (d *Decoder) H() uint32 { return d.h }

// allocateBuffers ports opj_t1_allocate_buffers for the HT path: (re)allocate
// and zero the working sample buffer and the significance/line-state scratch.
func (d *Decoder) allocateBuffers(w, h uint32) {
	datasize := int(w * h)
	if datasize > cap(d.data) {
		d.data = make([]uint32, datasize)
		d.out = make([]int32, datasize)
	} else {
		d.data = d.data[:datasize]
		d.out = d.out[:datasize]
		clear(d.data)
	}

	if d.sigma1 == nil {
		d.sigma1 = make([]uint32, 132)
		d.sigma2 = make([]uint32, 132)
		d.mbr1 = make([]uint32, 132)
		d.mbr2 = make([]uint32, 132)
		d.lineState = make([]uint8, 528)
	} else {
		clear(d.sigma1)
		clear(d.sigma2)
		clear(d.mbr1)
		clear(d.mbr2)
		clear(d.lineState)
	}

	d.w = w
	d.h = h
}

// decSample decodes one significant sample: fetches MagSgn data, evaluates m_n
// (the number of magnitude bits) using the EMB e_k bit, composes the sample
// value into data[idx] (sign bit 31 | ((2*mu + 0.5) << (p-1))) and returns v_n
// (before the leading-bit-count reduction) for the line-state E update.
//
// embBit is the EMB e_k bit (subtracted from U_q to form m_n); e1Bit is the EMB
// e_1 bit added as the MSB; p is cblk->numbps.
func (d *Decoder) decSample(ms *frwdStruct, uq, embBit, e1Bit, p uint32, idx int) uint32 {
	msVal := ms.fetch()
	mN := uq - embBit
	ms.advance(mN)
	val := msVal << 31
	vN := msVal & ((uint32(1) << mN) - 1)
	vN |= e1Bit << mN
	vN |= 1
	d.data[idx] = val | ((vN + 2) << (p - 1))
	return vN
}

// decodeSamples decodes the eight samples of one quad pair (two quads of 2x2)
// and updates the line state. It ports the identical MagSgn/line-state block
// used by both the initial-two-rows loop and the non-initial-rows loop of
// opj_t1_ht_decode_cblk. q0/q1 are the quad infos, uq0/uq1 the U_q values, p is
// numbps, locs is the in-block sample mask, spIdx the sample index of the quad
// pair's first sample, lspIdx the line-state index, stride the row stride.
func (d *Decoder) decodeSamples(ms *frwdStruct, q0, q1, uq0, uq1, p, locs uint32, spIdx, lspIdx, stride int) {
	data := d.data
	ls := d.lineState
	var vN uint32

	// --- first quad ---
	if q0&0x10 != 0 {
		d.decSample(ms, uq0, (q0>>12)&1, (q0&0x100)>>8, p, spIdx)
	} else if locs&0x1 != 0 {
		data[spIdx] = 0
	}

	if q0&0x20 != 0 {
		vN = d.decSample(ms, uq0, (q0>>13)&1, (q0&0x200)>>9, p, spIdx+stride)
		t := uint32(ls[lspIdx]) & 0x7F
		e := uint32(bits.Len32(vN))
		ls[lspIdx] = uint8(0x80 | max(t, e))
	} else if locs&0x2 != 0 {
		data[spIdx+stride] = 0
	}

	lspIdx++
	spIdx++

	if q0&0x40 != 0 {
		d.decSample(ms, uq0, (q0>>14)&1, (q0&0x400)>>10, p, spIdx)
	} else if locs&0x4 != 0 {
		data[spIdx] = 0
	}

	ls[lspIdx] = 0
	if q0&0x80 != 0 {
		vN = d.decSample(ms, uq0, (q0>>15)&1, (q0&0x800)>>11, p, spIdx+stride)
		ls[lspIdx] = uint8(0x80 | bits.Len32(vN))
	} else if locs&0x8 != 0 {
		data[spIdx+stride] = 0
	}

	spIdx++

	// --- second quad ---
	if q1&0x10 != 0 {
		d.decSample(ms, uq1, (q1>>12)&1, (q1&0x100)>>8, p, spIdx)
	} else if locs&0x10 != 0 {
		data[spIdx] = 0
	}

	if q1&0x20 != 0 {
		vN = d.decSample(ms, uq1, (q1>>13)&1, (q1&0x200)>>9, p, spIdx+stride)
		t := uint32(ls[lspIdx]) & 0x7F
		e := uint32(bits.Len32(vN))
		ls[lspIdx] = uint8(0x80 | max(t, e))
	} else if locs&0x20 != 0 {
		data[spIdx+stride] = 0
	}

	lspIdx++
	spIdx++

	if q1&0x40 != 0 {
		d.decSample(ms, uq1, (q1>>14)&1, (q1&0x400)>>10, p, spIdx)
	} else if locs&0x40 != 0 {
		data[spIdx] = 0
	}

	ls[lspIdx] = 0
	if q1&0x80 != 0 {
		vN = d.decSample(ms, uq1, (q1>>15)&1, (q1&0x800)>>11, p, spIdx+stride)
		ls[lspIdx] = uint8(0x80 | bits.Len32(vN))
	} else if locs&0x80 != 0 {
		data[spIdx+stride] = 0
	}
	_ = vN
}

// DecodeCblk ports opj_t1_ht_decode_cblk: decode one High-Throughput code-block
// into Data() (raster order), or into cblk.DecodedData when that field is set.
//
// cblk carries the coded chunks and segment layout (the same t1.CodeBlockDec
// model consumed by t1.DecodeCblk). orient is ignored (the same decoder serves
// all sub-bands). roishift must be 0 (HT decoding does not support ROI).
// cblksty is the code-block style bitmask (only VSC / stripe-causal matters
// here). mb is cblk->Mb (Kmax = band->numbps), which the t1.CodeBlockDec type
// does not carry and tcd must supply. em receives warnings/errors.
//
// Returns ok=false (with the message already emitted via em) for every
// malformed-stream condition where the C reference returns OPJ_FALSE; ok=true
// otherwise. The error return is always nil (kept for signature symmetry with
// t1.DecodeCblk); malformed streams are reported through em, matching C.
func (d *Decoder) DecodeCblk(cblk *t1.CodeBlockDec, orient, roishift, cblksty, mb uint32, em *event.Manager) (bool, error) {
	_ = orient // the same decoder is used for all sub-bands

	if roishift != 0 {
		return fail(em, "We do not support ROI in decoding HT codeblocks\n")
	}

	width := int(cblk.X1 - cblk.X0)
	height := int(cblk.Y1 - cblk.Y0)
	// Validate geometry before allocating or indexing (the C reference relies on
	// asserts / upstream guarantees; per the no-panic rule we check explicitly).
	if width <= 0 || height <= 0 || width > 1024 || height > 1024 || width*height > 4096 {
		return fail(em, "Malformed HT codeblock. Invalid code-block dimensions "+
			"%dx%d.\n", width, height)
	}
	d.allocateBuffers(uint32(width), uint32(height))
	d.result = d.out
	if cblk.DecodedData != nil {
		if len(cblk.DecodedData) < width*height {
			return fail(em, "Malformed HT codeblock. DecodedData buffer too "+
				"small.\n")
		}
		d.result = cblk.DecodedData
	}

	if mb == 0 {
		// Empty code-block: leave the (zeroed) buffer, convert to nothing.
		d.finalize(width, height, width)
		return true, nil
	}

	// numbps = Mb + 1 - zero_bplanes; zero_bplanes = missing_msbs.
	zeroBplanes := (mb + 1) - cblk.Numbps

	if cblk.NumChunks == 0 {
		d.finalize(width, height, width)
		return true, nil
	}

	// Compute the whole code-block length from the chunk lengths, validating
	// that each chunk actually holds the claimed number of bytes.
	cblkLen := uint32(0)
	for i := uint32(0); i < cblk.NumChunks; i++ {
		if int(i) >= len(cblk.Chunks) {
			return fail(em, "Malformed HT codeblock. Missing chunk data.\n")
		}
		n := cblk.Chunks[i].Len
		if int(n) > len(cblk.Chunks[i].Data) {
			return fail(em, "Malformed HT codeblock. Chunk length exceeds "+
				"available data.\n")
		}
		cblkLen += n
	}

	// Concatenate all chunks (+ a small slack so the forward 4-byte readers
	// never index out of bounds near the tail).
	needed := int(cblkLen) + 8
	if cap(d.cblkBuf) < needed {
		d.cblkBuf = make([]byte, needed)
	}
	d.cblkBuf = d.cblkBuf[:needed]
	off := 0
	for i := uint32(0); i < cblk.NumChunks; i++ {
		n := int(cblk.Chunks[i].Len)
		copy(d.cblkBuf[off:off+n], cblk.Chunks[i].Data[:n])
		off += n
	}
	for i := off; i < needed; i++ {
		d.cblkBuf[i] = 0
	}
	codedData := d.cblkBuf

	// num_passes: 1 (CUP), 2 (CUP+SPP), or 3 (CUP+SPP+MRP).
	numPasses := uint32(0)
	if cblk.RealNumSegs > 0 {
		numPasses = cblk.Segs[0].RealNumPasses
	}
	if cblk.RealNumSegs > 1 {
		numPasses += cblk.Segs[1].RealNumPasses
	}
	lengths1 := uint32(0)
	if numPasses > 0 {
		lengths1 = cblk.Segs[0].Len
	}
	lengths2 := uint32(0)
	if numPasses > 1 {
		lengths2 = cblk.Segs[1].Len
	}
	stride := width

	if numPasses > 1 && lengths2 == 0 {
		em.Warnf("A malformed codeblock that has more than one coding pass, " +
			"but zero length for 2nd and potentially the 3rd pass in an HT codeblock.\n")
		numPasses = 1
	}
	if numPasses > 3 {
		return fail(em, "We do not support more than 3 coding passes in an HT "+
			"codeblock; This codeblocks has %d passes.\n", numPasses)
	}

	if mb > 30 {
		return fail(em, "32 bits are not enough to decode this codeblock, since "+
			"the number of bitplane, %d, is larger than 30.\n", mb)
	}
	if zeroBplanes > mb {
		return fail(em, "Malformed HT codeblock. Decoding this codeblock is "+
			"stopped. There are %d zero bitplanes in %d bitplanes.\n", zeroBplanes, mb)
	} else if zeroBplanes == mb && numPasses > 1 {
		onlyCleanupPassWarned.Do(func() {
			em.Warnf("Malformed HT codeblock. When the number of zero planes "+
				"bitplanes is equal to the number of bitplanes, only the cleanup "+
				"pass makes sense, but we have %d passes in this codeblock. "+
				"Therefore, only the cleanup pass will be decoded. This message "+
				"will not be displayed again.\n", numPasses)
		})
		numPasses = 1
	}

	p := cblk.Numbps
	zeroBplanesP1 := zeroBplanes + 1

	if lengths1 < 2 || lengths1 > cblkLen || (lengths1+lengths2) > cblkLen {
		return fail(em, "Malformed HT codeblock. Invalid codeblock length "+
			"values.\n")
	}

	lcup := int(lengths1)
	scup := (int(codedData[lcup-1]) << 4) + int(codedData[lcup-2]&0xF)
	if scup < 2 || scup > lcup || scup > 4079 {
		return fail(em, "Malformed HT codeblock. One of the following condition "+
			"is not met: 2 <= Scup <= min(Lcup, 4079)\n")
	}

	var mel decMel
	var vlc, magref revStruct
	var magsgn, sigprop frwdStruct

	if !mel.init(codedData, lcup, scup) {
		return fail(em, "Malformed HT codeblock. Incorrect MEL segment "+
			"sequence.\n")
	}
	vlc.initVLC(codedData, lcup, scup)
	magsgn.init(codedData, 0, lcup-scup, 0xFF)
	if numPasses > 1 {
		sigprop.init(codedData, int(lengths1), int(lengths2), 0)
	}
	if numPasses > 2 {
		magref.initMRP(codedData, int(lengths1), int(lengths2))
	}

	stripeCausal := (cblksty & t1.CblkstyVSC) != 0

	// --- cleanup pass (+ SigProp / MagRef refinement passes) ---
	if err := d.decodeCleanup(&mel, &vlc, &magref, &magsgn, &sigprop,
		width, height, stride, p, zeroBplanesP1, numPasses, stripeCausal, em); err != nil {
		return false, err
	}

	d.finalize(width, height, stride)
	return true, nil
}

// finalize ports the trailing sign-magnitude to two's-complement conversion.
func (d *Decoder) finalize(width, height, stride int) {
	data := d.data
	res := d.result
	for y := 0; y < height; y++ {
		row := y * stride
		for x := 0; x < width; x++ {
			s := data[row+x]
			val := int32(s & 0x7FFFFFFF)
			if s&0x80000000 != 0 {
				res[row+x] = -val
			} else {
				res[row+x] = val
			}
		}
	}
}
