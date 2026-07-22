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

// ---------------------------------------------------------------------------
// Register-resident decode (DOWNLOAD/UPLOAD_MQC_VARIABLES pattern)
// ---------------------------------------------------------------------------
//
// The C reference macro-inlines the whole MQ decoder into each tier-1 pass,
// keeping a/c/ct/bp (and the current-context pointer) in CPU registers across
// the entire pass via DOWNLOAD_MQC_VARIABLES / UPLOAD_MQC_VARIABLES. Go cannot
// inline the method form of Decode (its renormalization contains a loop, and
// the per-call traffic through the *MQC pointer defeats register residency).
//
// DecState mirrors the DOWNLOAD_MQC_VARIABLES register block. The tier-1 hot
// passes load it once (LoadDec), thread it by value through the pass — Go's
// register ABI keeps the fields in registers because its address is never
// taken — and store it back once (StoreDec). DecodeReg is the inlinable decode
// fast path (the renorm loop is factored into renormDec, the only call), so it
// expands into the pass loops exactly as opj_mqc_decode_macro does.
//
// These produce bit-identical state transitions to Decode/renormd/bytein; the
// method forms are retained for the mqc API, the RAW path and the vector tests.

// DecState is the register block of the MQ decoder (mqc->a/c/ct plus the bp
// offset), used for register-resident decoding across a whole tier-1 pass.
type DecState struct {
	A  uint32 // interval register (mqc->a)
	C  uint32 // code register (mqc->c)
	Ct uint32 // bit counter (mqc->ct)
	Bp int    // input position (offset form of mqc->bp)
}

// LoadDec captures the current decoder registers (DOWNLOAD_MQC_VARIABLES).
func (m *MQC) LoadDec() DecState {
	return DecState{A: m.a, C: m.c, Ct: m.ct, Bp: m.bp}
}

// StoreDec writes back decoder registers held in locals (UPLOAD_MQC_VARIABLES).
func (m *MQC) StoreDec(s DecState) {
	m.a = s.A
	m.c = s.C
	m.ct = s.Ct
	m.bp = s.Bp
}

// Ctxs returns a pointer to the context-state-index array so the hot passes can
// select and update the active context directly (the C code holds a pointer
// into mqc->ctxs); mutations remain visible to ResetStates/SetState.
func (m *MQC) Ctxs() *[numCtxs]int32 { return &m.ctxs }

// renormDec is the register-resident port of opj_mqc_renormd_macro with
// opj_mqc_bytein_macro inlined; it owns the loop that blocks inlining of the
// decode, so DecodeReg (its only caller besides itself) stays inlinable.
func (m *MQC) renormDec(s DecState) DecState {
	buf := m.buf
	for {
		if s.Ct == 0 {
			// opj_mqc_bytein_macro
			lc := buf[s.Bp+1]
			if buf[s.Bp] == 0xff {
				if lc > 0x8f {
					s.C += 0xff00
					s.Ct = 8
					m.endOfByteStreamCounter++
				} else {
					s.Bp++
					s.C += uint32(lc) << 9
					s.Ct = 7
				}
			} else {
				s.Bp++
				s.C += uint32(lc) << 8
				s.Ct = 8
			}
		}
		s.A <<= 1
		s.C <<= 1
		s.Ct--
		if s.A >= 0x8000 {
			return s
		}
	}
}

// DecodeReg is the register-resident port of opj_mqc_decode_macro: it decodes
// one decision against ctxs[curctx] using the register block s, updates the
// context in place, and returns the decoded symbol plus the advanced registers.
// It is inlinable (the renorm loop lives in renormDec), so it expands into the
// tier-1 pass loops the way the C macro does.
func (m *MQC) DecodeReg(s DecState, ctxs *[numCtxs]int32, curctx int) (uint32, DecState) {
	var d uint32
	st := &states[ctxs[curctx]]
	s.A -= st.qeval
	if (s.C >> 16) < st.qeval {
		// opj_mqc_lpsexchange_macro
		if s.A < st.qeval {
			s.A = st.qeval
			d = st.mps
			ctxs[curctx] = st.nmps
		} else {
			s.A = st.qeval
			d = 1 - st.mps
			ctxs[curctx] = st.nlps
		}
	} else {
		s.C -= st.qeval << 16
		if (s.A & 0x8000) != 0 {
			// MPS, no renormalization needed (the common fast path).
			return st.mps, s
		}
		// opj_mqc_mpsexchange_macro
		if s.A < st.qeval {
			d = 1 - st.mps
			ctxs[curctx] = st.nlps
		} else {
			d = st.mps
			ctxs[curctx] = st.nmps
		}
	}
	// Single renormalization site (the only remaining call) keeps DecodeReg
	// small enough for the inliner, so it expands into the tier-1 pass loops.
	return d, m.renormDec(s)
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
