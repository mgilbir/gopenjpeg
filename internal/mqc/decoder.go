package mqc

import "errors"

// ErrShortBuffer reports a decoder buffer without the required
// cblkDataExtra writable scratch bytes past the coded length.
var ErrShortBuffer = errors.New("mqc: decoder buffer needs at least len+2 writable bytes")

// This file ports the decoder half of mqc.c / mqc_inl.h.
//
// Buffer model. In the C reference the decoder temporarily overwrites the
// OPJ_COMMON_CBLK_DATA_EXTRA (=2) bytes past the coded data with an artificial
// 0xFF 0xFF marker so the bytein routine can stop on it without comparing
// pointers, backing them up first and restoring them in FinishDec. We mirror
// that exactly: the caller must pass a slice with at least length+2 writable
// bytes; InitDec/RawInitDec overwrite buf[length] and buf[length+1] and
// FinishDec restores them.

// initDecCommon is the port of opj_mqc_init_dec_common.
//
// buf must have at least length+cblkDataExtra bytes; the two bytes at
// buf[length] and buf[length+1] are backed up and overwritten with 0xFF.
func (m *MQC) initDecCommon(buf []byte, length int) error {
	if length < 0 || len(buf) < length+cblkDataExtra {
		// Faithful to the C contract (assert extra_writable_bytes >= EXTRA),
		// surfaced as an error rather than a panic or out-of-bounds access.
		return ErrShortBuffer
	}
	m.buf = buf
	m.start = 0
	m.end = length
	m.bp = 0
	// Backup the bytes we will overwrite, then insert the fake 0xFF 0xFF
	// marker so bytein stops on it.
	copy(m.backup[:], buf[length:length+cblkDataExtra])
	buf[length] = 0xff
	buf[length+1] = 0xff
	return nil
}

// InitDec is the port of opj_mqc_init_dec: initialise the decoder for MQ
// decoding (ISO 15444-1 C.3.5 INITDEC).
//
// FinishDec must be called after decoding to restore the overwritten bytes.
func (m *MQC) InitDec(buf []byte, length int) error {
	if err := m.initDecCommon(buf, length); err != nil {
		return err
	}
	m.SetCurCtx(0)
	m.endOfByteStreamCounter = 0
	if length == 0 {
		m.c = 0xff << 16
	} else {
		m.c = uint32(m.buf[m.bp]) << 16
	}

	m.bytein()
	m.c <<= 7
	m.ct -= 7
	m.a = 0x8000
	return nil
}

// RawInitDec is the port of opj_mqc_raw_init_dec: initialise the decoder for
// RAW decoding.
//
// FinishDec must be called after decoding to restore the overwritten bytes.
func (m *MQC) RawInitDec(buf []byte, length int) error {
	if err := m.initDecCommon(buf, length); err != nil {
		return err
	}
	m.c = 0
	m.ct = 0
	return nil
}

// FinishDec is the port of opq_mqc_finish_dec (sic — the C name has a typo):
// restore the bytes temporarily overwritten by InitDec/RawInitDec.
func (m *MQC) FinishDec() {
	copy(m.buf[m.end:m.end+cblkDataExtra], m.backup[:])
}

// bytein is the port of opj_mqc_bytein / opj_mqc_bytein_macro: input a byte,
// handling the 0xFF stuffing and the terminating 0xFF >0x8F end-of-stream
// rule. Relies on the fake 0xFF 0xFF marker inserted by initDecCommon.
func (m *MQC) bytein() {
	lc := m.buf[m.bp+1]
	if m.buf[m.bp] == 0xff {
		if lc > 0x8f {
			m.c += 0xff00
			m.ct = 8
			m.endOfByteStreamCounter++
		} else {
			m.bp++
			m.c += uint32(lc) << 9
			m.ct = 7
		}
	} else {
		m.bp++
		m.c += uint32(lc) << 8
		m.ct = 8
	}
}

// renormd is the port of opj_mqc_renormd_macro.
func (m *MQC) renormd() {
	for {
		if m.ct == 0 {
			m.bytein()
		}
		m.a <<= 1
		m.c <<= 1
		m.ct--
		if m.a >= 0x8000 {
			break
		}
	}
}

// Decode is the port of opj_mqc_decode / opj_mqc_decode_macro: decode a single
// decision using the currently selected context (ISO 15444-1 C.3.2 DECODE).
// It returns the decoded symbol (0 or 1).
//
// st is taken by pointer to avoid copying the 16-byte mqcState per decision.
// (The whole function cannot be inlined into the tier-1 pass loops because the
// renormalization it calls contains a loop; see the worker report's note on the
// single-thread tier-1 gap versus the C macro-inlined coder.)
func (m *MQC) Decode() uint32 {
	var d uint32
	st := &states[uint32(m.ctxs[m.curctx])]
	m.a -= st.qeval
	if (m.c >> 16) < st.qeval {
		// LPS exchange (opj_mqc_lpsexchange_macro).
		if m.a < st.qeval {
			m.a = st.qeval
			d = st.mps
			m.ctxs[m.curctx] = st.nmps
		} else {
			m.a = st.qeval
			d = 1 - st.mps
			m.ctxs[m.curctx] = st.nlps
		}
		m.renormd()
	} else {
		m.c -= st.qeval << 16
		if (m.a & 0x8000) == 0 {
			// MPS exchange (opj_mqc_mpsexchange_macro).
			if m.a < st.qeval {
				d = 1 - st.mps
				m.ctxs[m.curctx] = st.nlps
			} else {
				d = st.mps
				m.ctxs[m.curctx] = st.nmps
			}
			m.renormd()
		} else {
			d = st.mps
		}
	}
	return d
}

// RawDecode is the port of opj_mqc_raw_decode: decode a single bit using the
// raw decoder (cf. p.506 Taubman). Returns the decoded symbol (0 or 1).
func (m *MQC) RawDecode() uint32 {
	if m.ct == 0 {
		// Given RawInitDec we know a 0xFF 0xFF artificial marker exists.
		if m.c == 0xff {
			if m.buf[m.bp] > 0x8f {
				m.c = 0xff
				m.ct = 8
			} else {
				m.c = uint32(m.buf[m.bp])
				m.bp++
				m.ct = 7
			}
		} else {
			m.c = uint32(m.buf[m.bp])
			m.bp++
			m.ct = 8
		}
	}
	m.ct--
	return (m.c >> m.ct) & 0x01
}
