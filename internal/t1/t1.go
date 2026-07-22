package t1

import "github.com/mgilbir/gopenjpeg/internal/mqc"

// T1 is the Go equivalent of opj_t1_t: the state of a tier-1 coder/decoder.
// A single T1 can be reused across many code-blocks (its data/flags buffers
// grow on demand), mirroring the per-thread reuse in the C reference.
type T1 struct {
	// mqc is the shared MQ/RAW arithmetic coder (opj_t1_t.mqc).
	mqc mqc.MQC

	// data holds the code-block coefficients (opj_t1_t.data). During encoding
	// it is filled in "zigzag" (column-of-4) order; during decoding it is
	// written in natural raster order. Length is w*h.
	data []int32
	// flags holds the packed flag words (opj_t1_t.flags), one per 4-row column
	// plus a 1-word border on every side. Indexed as
	// flags[x+1 + ((y/4)+1)*(w+2)].
	flags []uint32

	w uint32 // opj_t1_t.w
	h uint32 // opj_t1_t.h

	datasize  uint32 // opj_t1_t.datasize (capacity of data, in elements)
	flagssize uint32 // opj_t1_t.flagssize (capacity of flags, in words)

	encoder bool // opj_t1_t.encoder

	// cblkdatabuffer is a reusable scratch buffer used by DecodeCblk to
	// concatenate a code-block's chunks plus the 2 trailing MQ scratch bytes
	// (opj_t1_t.cblkdatabuffer); it grows on demand and is reused across
	// code-blocks to keep decoding allocation-free.
	cblkdatabuffer []byte

	// lutCtxnoZCOrient is lutCtxnoZC[orient<<9:], the orient-selected zero
	// coding context slice (opj_mqc_t.lut_ctxno_zc_orient in the C source; kept
	// on the T1 here since internal/mqc does not expose that field).
	lutCtxnoZCOrient []uint8

	// PtermWarning records the last predictable-termination check message when
	// DecodeCblk is called with checkPterm=true (the C reference emits these
	// through the event manager). Empty means the segment terminated cleanly.
	PtermWarning string
}

// New creates a T1 handle (port of opj_t1_create). isEncoder selects the
// encode-vs-decode role, matching the C flag; buffers are allocated lazily.
func New(isEncoder bool) *T1 {
	return &T1{encoder: isEncoder}
}

// Data returns the current coefficient buffer (t1->data), valid for the most
// recently coded code-block. For decoding this is the reconstructed t1->data
// array (raster order) before ROI de-scaling / dequantization.
func (t *T1) Data() []int32 { return t.data }

// W returns the width of the current code-block.
func (t *T1) W() uint32 { return t.w }

// H returns the height of the current code-block.
func (t *T1) H() uint32 { return t.h }

// allocateBuffers ports opj_t1_allocate_buffers: (re)allocate and zero the data
// and flags buffers for a w x h code-block, installing the border sentinel
// words. Preconditions (per the JPEG 2000 spec) w<=1024, h<=1024, w*h<=4096.
func (t *T1) allocateBuffers(w, h uint32) {
	// data: encoder uses the tile buffer in the C code, but for this
	// standalone port the caller fills t1.data via SetData, so we always keep a
	// zeroed w*h buffer available.
	datasize := w * h
	if datasize > t.datasize {
		t.data = make([]int32, datasize)
		t.datasize = datasize
	} else {
		clear(t.data[:datasize])
	}

	flagsStride := w + 2 // can't be 0
	flagssize := ((h+3)/4 + 2) * flagsStride

	if flagssize > t.flagssize {
		t.flags = make([]uint32, flagssize)
	} else {
		clear(t.flags[:flagssize])
	}
	t.flagssize = flagssize

	flagsHeight := (h + 3) / 4

	// Top border row: sentinel PI bits so passes ignore it.
	for x := uint32(0); x < flagsStride; x++ {
		t.flags[x] = t1Pi0 | t1Pi1 | t1Pi2 | t1Pi3
	}
	// Bottom border row.
	base := (flagsHeight + 1) * flagsStride
	for x := uint32(0); x < flagsStride; x++ {
		t.flags[base+x] = t1Pi0 | t1Pi1 | t1Pi2 | t1Pi3
	}
	// Partial last row: mask out the PI bits for the rows that do not exist.
	if h%4 != 0 {
		var v uint32
		switch h % 4 {
		case 1:
			v = t1Pi1 | t1Pi2 | t1Pi3
		case 2:
			v = t1Pi2 | t1Pi3
		case 3:
			v = t1Pi3
		}
		p := flagsHeight * flagsStride
		for x := uint32(0); x < flagsStride; x++ {
			t.flags[p+x] = v
		}
	}

	t.w = w
	t.h = h
}

// SetData installs the code-block coefficient buffer (w*h elements) for the
// next EncodeCblk call, mirroring the way opj_t1_cblk_encode_processor fills
// t1->data from the tile buffer before calling opj_t1_encode_cblk. The values
// must already be in T1 "zigzag" order and fixed-point-shifted exactly as the
// C processor produces them; the slice is used directly (not copied).
//
// It must be called after the geometry is known; callers that drive EncodeCblk
// with an explicit CodeBlockEnc should size the data to (x1-x0)*(y1-y0).
func (t *T1) SetData(data []int32, w, h uint32) {
	t.allocateBuffers(w, h)
	copy(t.data, data)
}
