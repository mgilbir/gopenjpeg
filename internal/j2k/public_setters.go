package j2k

// This file holds additive exported setters/getters used by the JP2 container
// adapter (the root gopenjpeg package wires j2k.Decoder to
// jp2.CodestreamCodec). Each ports a direct field access jp2.c performs on
// jp2->j2k. They are additive: no existing decode behaviour changes.

// SetAllowDifferentBitDepthSign ports the assignment
//
//	jp2->j2k->m_cp.allow_different_bit_depth_sign = (jp2->bpc == 255)
//
// in opj_jp2_read_ihdr.
func (d *Decoder) SetAllowDifferentBitDepthSign(allow bool) {
	d.CP.AllowDifferentBitDepthSign = allow
}

// SetIHDRDimensions ports the assignments
//
//	jp2->j2k->ihdr_w = jp2->w
//	jp2->j2k->ihdr_h = jp2->h
//
// in opj_jp2_read_ihdr. The decode path records these for parity with the C
// reference; the current marker parsers do not additionally validate SIZ
// against them, so the values are informational.
func (d *Decoder) SetIHDRDimensions(w, h uint32) {
	d.ihdrW = w
	d.ihdrH = h
}

// NumCompsToDecode ports the read of
// jp2->j2k->m_specific_param.m_decoder.m_numcomps_to_decode in
// opj_jp2_apply_color_postprocessing (a non-zero value bypasses the JP2 colour
// transforms).
func (d *Decoder) NumCompsToDecode() uint32 {
	return d.dec.numcompsToDecode
}
