package t1

// This file defines the minimal local types that tier-1 touches. They mirror
// only the fields of the corresponding opj_tcd_* structures that t1.c reads or
// writes; the future tcd worker is expected to adapt (embed or map onto) these.

// Chunk mirrors opj_tcd_seg_data_chunk_t: one contiguous piece of a
// code-block's coded data as handed over by tier-2.
type Chunk struct {
	Data []byte
	Len  uint32
}

// Seg mirrors the fields of opj_tcd_seg_t consumed by opj_t1_decode_cblk.
type Seg struct {
	// Len is the number of coded bytes in this segment (seg->len).
	Len uint32
	// RealNumPasses is the number of coding passes actually present in this
	// segment (seg->real_num_passes).
	RealNumPasses uint32
}

// CodeBlockDec mirrors the fields of opj_tcd_cblk_dec_t read by
// opj_t1_decode_cblk (and the surrounding processor).
type CodeBlockDec struct {
	X0, Y0, X1, Y1 int32 // code-block bounds in the sub-band

	// Numbps is the number of bit-planes present (cblk->numbps).
	Numbps uint32

	// Chunks are the coded-data chunks (cblk->chunks). NumChunks is
	// cblk->numchunks. On decode they are concatenated (with 2 trailing scratch
	// bytes) into the MQ buffer.
	Chunks    []Chunk
	NumChunks uint32

	// Segs / RealNumSegs mirror cblk->segs and cblk->real_num_segs: the
	// segmentation of passes across MQ/RAW termination boundaries.
	Segs        []Seg
	RealNumSegs uint32

	// Corrupted mirrors cblk->corrupted: if set, decoding is skipped.
	Corrupted bool

	// DecodedData, when non-nil, mirrors cblk->decoded_data: decode targets this
	// buffer (sub-tile decoding) instead of the shared t1->data.
	DecodedData []int32
}

// Pass mirrors the fields of opj_tcd_pass_t written by opj_t1_encode_cblk.
type Pass struct {
	// Rate is the cumulative byte count reachable by truncating after this pass
	// (pass->rate).
	Rate uint32
	// DistortionDec is the cumulative weighted MSE reduction up to this pass
	// (pass->distortiondec).
	DistortionDec float64
	// Term is 1 if the pass is terminated, 0 otherwise (pass->term).
	Term int
	// Len is the number of bytes contributed by this pass (pass->len).
	Len uint32
}

// CodeBlockEnc mirrors the fields of opj_tcd_cblk_enc_t used by
// opj_t1_encode_cblk. Geometry (X0..Y1) sizes the code-block; Data receives the
// coded byte stream; Passes/Totalpasses/Numbps receive the pass metadata.
type CodeBlockEnc struct {
	X0, Y0, X1, Y1 int32 // code-block bounds

	// Data receives the coded byte stream (cblk->data). It is (re)filled by
	// EncodeCblk with exactly Totalpasses' worth of bytes.
	Data []byte

	// Passes receives per-pass metadata (cblk->passes); it is grown as needed.
	Passes []Pass

	// Numbps is the number of magnitude bit-planes (cblk->numbps), computed from
	// the data.
	Numbps uint32
	// Totalpasses is the number of coding passes produced (cblk->totalpasses).
	Totalpasses uint32
}
