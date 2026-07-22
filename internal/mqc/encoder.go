package mqc

// This file ports the encoder half of mqc.c / mqc_inl.h.
//
// Buffer model. In the C reference opj_mqc_init_enc(mqc, bp) sets mqc->bp =
// bp - 1 and mqc->start = bp, relying on the caller (tcd) having reserved one
// scratch byte before bp. We reproduce that here by owning a buffer whose
// index 0 is that scratch byte: start = 1 and bp is initialised to 0
// (== start - 1). Output bytes therefore live at buf[1:bp], and NumBytes()
// returns bp - start. The buffer grows on demand.

// InitEnc is the port of opj_mqc_init_enc. It prepares the coder to write into
// an internal, growable buffer. Use Bytes()/NumBytes() to read the output.
func (m *MQC) InitEnc() {
	// To avoid the curctx index dangling; not strictly required as the
	// current context is always set before encoding.
	m.SetCurCtx(0)

	// Figure C.10 - Initialization of the encoder (INITENC).
	m.a = 0x8000
	m.c = 0
	if cap(m.buf) < 1 {
		m.buf = make([]byte, 1, 4096)
	} else {
		m.buf = m.buf[:1]
	}
	// The fake byte before start must not be 0xff (matches the C assert).
	m.buf[0] = 0
	m.start = 1
	// Yes, we point before the start of the buffer, but this is safe.
	m.bp = 0 // start - 1
	m.ct = 12
	m.endOfByteStreamCounter = 0
}

// Bytes returns the encoded output produced since InitEnc. When nothing was
// encoded (bp still at the fake leading byte, i.e. a zero-pass code-block)
// it returns nil instead of slicing [start:start-1].
func (m *MQC) Bytes() []byte {
	if m.bp < m.start {
		return nil
	}
	return m.buf[m.start:m.bp]
}

// ensureEncCap grows buf so that it has at least n bytes, zeroing any newly
// exposed region. Only index 0 strictly needs to be zero (see byteout), but
// zeroing keeps reuse across codeblocks clean.
func (m *MQC) ensureEncCap(n int) {
	if n <= len(m.buf) {
		return
	}
	if n <= cap(m.buf) {
		old := len(m.buf)
		m.buf = m.buf[:n]
		for i := old; i < n; i++ {
			m.buf[i] = 0
		}
		return
	}
	grow := make([]byte, n-len(m.buf))
	m.buf = append(m.buf, grow...)
}

// byteout is the port of opj_mqc_byteout: output a byte, doing bit-stuffing if
// necessary (after a 0xff byte, the next byte must be < 0x90).
func (m *MQC) byteout() {
	m.ensureEncCap(m.bp + 2)
	if m.buf[m.bp] == 0xff {
		m.bp++
		m.buf[m.bp] = byte(m.c >> 20)
		m.c &= 0xfffff
		m.ct = 7
	} else {
		if (m.c & 0x8000000) == 0 {
			m.bp++
			m.buf[m.bp] = byte(m.c >> 19)
			m.c &= 0x7ffff
			m.ct = 8
		} else {
			m.buf[m.bp]++
			if m.buf[m.bp] == 0xff {
				m.c &= 0x7ffffff
				m.bp++
				m.buf[m.bp] = byte(m.c >> 20)
				m.c &= 0xfffff
				m.ct = 7
			} else {
				m.bp++
				m.buf[m.bp] = byte(m.c >> 19)
				m.c &= 0x7ffff
				m.ct = 8
			}
		}
	}
}

// renorme is the port of opj_mqc_renorme_macro.
func (m *MQC) renorme() {
	for {
		m.a <<= 1
		m.c <<= 1
		m.ct--
		if m.ct == 0 {
			m.byteout()
		}
		if (m.a & 0x8000) != 0 {
			break
		}
	}
}

// codemps is the port of opj_mqc_codemps_macro: encode the most probable
// symbol.
func (m *MQC) codemps() {
	st := states[m.ctxs[m.curctx]]
	m.a -= st.qeval
	if (m.a & 0x8000) == 0 {
		if m.a < st.qeval {
			m.a = st.qeval
		} else {
			m.c += st.qeval
		}
		m.ctxs[m.curctx] = st.nmps
		m.renorme()
	} else {
		m.c += st.qeval
	}
}

// codelps is the port of opj_mqc_codelps_macro: encode the least probable
// symbol.
func (m *MQC) codelps() {
	st := states[m.ctxs[m.curctx]]
	m.a -= st.qeval
	if m.a < st.qeval {
		m.c += st.qeval
	} else {
		m.a = st.qeval
	}
	m.ctxs[m.curctx] = st.nlps
	m.renorme()
}

// Encode is the port of opj_mqc_encode / opj_mqc_encode_macro: encode the
// symbol d (0 or 1) using the currently selected context.
func (m *MQC) Encode(d uint32) {
	if states[m.ctxs[m.curctx]].mps == d {
		m.codemps()
	} else {
		m.codelps()
	}
}

// ---------------------------------------------------------------------------
// Register-resident encode (DOWNLOAD/UPLOAD_MQC_VARIABLES pattern)
// ---------------------------------------------------------------------------
//
// The C reference macro-inlines the whole MQ encoder into each tier-1 pass,
// keeping a/c/ct/bp in CPU registers across the entire pass. This mirrors the
// decode-side DecState machinery (see decoder.go): the tier-1 hot encode passes
// load the registers once (LoadEnc), thread them by value through the pass —
// Go's register ABI keeps the fields in registers because their address is
// never taken — and store them back once (StoreEnc). EncodeReg is the inlinable
// encode fast path (the renormalization loop is factored into renormeEnc, its
// only call, exactly as opj_mqc_encode_macro expands).
//
// Buffer-growth boundary. In the C coder bp is a raw pointer into the output
// buffer, so growing/reallocating that buffer mid-pass would invalidate a bp
// held in a register — the delicacy W17 flagged when deferring this. In this Go
// port bp is an *integer offset* into m.buf, not a pointer, so a reallocation
// leaves the held Bp valid: only m.buf's slice header changes, and byteoutEnc
// re-reads m.buf fresh on every call (it never caches the slice in the hot
// loop). Growth therefore happens safely at the byteout boundary (where ct
// wraps), the one place the buffer is touched, and needs no pointer fixup. The
// hot EncodeReg path never touches m.buf at all (the MPS fast path is pure
// register arithmetic), so bp residency costs nothing there.
//
// These produce bit-identical state transitions and output bytes to
// Encode/codemps/codelps/renorme/byteout; the method forms are retained for the
// mqc API, the RAW/bypass path, flush/termination and the vector tests.

// EncState is the register block of the MQ encoder (mqc->a/c/ct plus the bp
// offset), used for register-resident encoding across a whole tier-1 pass.
type EncState struct {
	A  uint32 // interval register (mqc->a)
	C  uint32 // code register (mqc->c)
	Ct uint32 // free-bit counter (mqc->ct)
	Bp int    // output position (offset form of mqc->bp)
}

// LoadEnc captures the current encoder registers (DOWNLOAD_MQC_VARIABLES).
func (m *MQC) LoadEnc() EncState {
	return EncState{A: m.a, C: m.c, Ct: m.ct, Bp: m.bp}
}

// StoreEnc writes back encoder registers held in locals (UPLOAD_MQC_VARIABLES).
func (m *MQC) StoreEnc(s EncState) {
	m.a = s.A
	m.c = s.C
	m.ct = s.Ct
	m.bp = s.Bp
}

// byteoutEnc is the register-resident port of opj_mqc_byteout. It owns all
// buffer access for the register path: it re-reads m.buf fresh (so a grow is
// safe with the offset-form Bp) and grows on demand exactly like the method
// byteout. It is only called from renormeEnc (the cold ct-wrap boundary).
func (m *MQC) byteoutEnc(s EncState) EncState {
	m.ensureEncCap(s.Bp + 2)
	if m.buf[s.Bp] == 0xff {
		s.Bp++
		m.buf[s.Bp] = byte(s.C >> 20)
		s.C &= 0xfffff
		s.Ct = 7
	} else {
		if (s.C & 0x8000000) == 0 {
			s.Bp++
			m.buf[s.Bp] = byte(s.C >> 19)
			s.C &= 0x7ffff
			s.Ct = 8
		} else {
			m.buf[s.Bp]++
			if m.buf[s.Bp] == 0xff {
				s.C &= 0x7ffffff
				s.Bp++
				m.buf[s.Bp] = byte(s.C >> 20)
				s.C &= 0xfffff
				s.Ct = 7
			} else {
				s.Bp++
				m.buf[s.Bp] = byte(s.C >> 19)
				s.C &= 0x7ffff
				s.Ct = 8
			}
		}
	}
	return s
}

// renormeEnc is the register-resident port of opj_mqc_renorme_macro with
// opj_mqc_byteout inlined via byteoutEnc; it owns the renormalization loop that
// blocks inlining of the encode, so EncodeReg (its only caller) stays inlinable.
func (m *MQC) renormeEnc(s EncState) EncState {
	for {
		s.A <<= 1
		s.C <<= 1
		s.Ct--
		if s.Ct == 0 {
			s = m.byteoutEnc(s)
		}
		if (s.A & 0x8000) != 0 {
			return s
		}
	}
}

// EncodeReg is the register-resident port of opj_mqc_encode_macro: it encodes
// one decision d (0 or 1) against ctxs[curctx] using the register block s,
// updates the context in place, and returns the advanced registers. It is
// inlinable (the renorm loop lives in renormeEnc), so it expands into the
// tier-1 pass loops the way the C macro does.
func (m *MQC) EncodeReg(s EncState, ctxs *[numCtxs]int32, curctx int, d uint32) EncState {
	st := &states[ctxs[curctx]]
	if st.mps == d {
		// opj_mqc_codemps_macro
		s.A -= st.qeval
		if (s.A & 0x8000) == 0 {
			if s.A < st.qeval {
				s.A = st.qeval
			} else {
				s.C += st.qeval
			}
			ctxs[curctx] = st.nmps
			return m.renormeEnc(s)
		}
		// Common fast path: no renormalization needed.
		s.C += st.qeval
		return s
	}
	// opj_mqc_codelps_macro
	s.A -= st.qeval
	if s.A < st.qeval {
		s.C += st.qeval
	} else {
		s.A = st.qeval
	}
	ctxs[curctx] = st.nlps
	return m.renormeEnc(s)
}

// setbits is the port of opj_mqc_setbits: fill mqc->c with 1s for flushing.
func (m *MQC) setbits() {
	tempc := m.c + m.a
	m.c |= 0xffff
	if m.c >= tempc {
		m.c -= 0x8000
	}
}

// Flush is the port of opj_mqc_flush: terminate coding (C.2.9 / Figure C.11).
func (m *MQC) Flush() {
	m.setbits()
	m.c <<= m.ct
	m.byteout()
	m.c <<= m.ct
	m.byteout()

	// It is forbidden that a coding pass ends with 0xff.
	if m.buf[m.bp] != 0xff {
		m.bp++
	}
}

// BypassInitEnc is the port of opj_mqc_bypass_init_enc: BYPASS mode switch,
// initialization operation. Normally called after at least one Flush().
func (m *MQC) BypassInitEnc() {
	m.c = 0
	// In theory ct should be 8, but the sentinel BYPASS_CT_INIT records that
	// BypassEnc has never been called (avoids the 0xff 0x7f elimination trick
	// in BypassFlushEnc firing when no bit was output).
	m.ct = bypassCTInit
}

// BypassEnc is the port of opj_mqc_bypass_enc / opj_mqc_bypass_enc_macro:
// BYPASS mode coding operation.
func (m *MQC) BypassEnc(d uint32) {
	if m.ct == bypassCTInit {
		m.ct = 8
	}
	m.ct--
	m.c = m.c + (d << m.ct)
	if m.ct == 0 {
		m.ensureEncCap(m.bp + 2)
		m.buf[m.bp] = byte(m.c)
		m.ct = 8
		// If the previous byte was 0xff, make sure the next msb is 0.
		if m.buf[m.bp] == 0xff {
			m.ct = 7
		}
		m.bp++
		m.c = 0
	}
}

// BypassGetExtraBytes is the port of opj_mqc_bypass_get_extra_bytes.
func (m *MQC) BypassGetExtraBytes(erterm bool) uint32 {
	if m.ct < 7 || (m.ct == 7 && (erterm || m.buf[m.bp-1] != 0xff)) {
		return 1
	}
	return 0
}

// BypassFlushEnc is the port of opj_mqc_bypass_flush_enc: BYPASS mode flush
// operation, with the 0xff stuffing/elimination rules.
func (m *MQC) BypassFlushEnc(erterm bool) {
	if m.ct < 7 || (m.ct == 7 && (erterm || m.buf[m.bp-1] != 0xff)) {
		// Fill the remaining lsbs with an alternating 0,1,... sequence.
		var bitValue uint32
		for m.ct > 0 {
			m.ct--
			m.c += bitValue << m.ct
			bitValue = 1 - bitValue
		}
		m.ensureEncCap(m.bp + 2)
		m.buf[m.bp] = byte(m.c)
		m.bp++
	} else if m.ct == 7 && m.buf[m.bp-1] == 0xff {
		// Discard last 0xff.
		m.bp--
	} else if m.ct == 8 && !erterm &&
		m.buf[m.bp-1] == 0x7f && m.buf[m.bp-2] == 0xff {
		// Discard terminating 0xff 0x7f: it is interpreted as
		// 0xff 0x7f [0xff 0xff] by the decoder.
		m.bp -= 2
	}
}

// ResetEnc is the port of opj_mqc_reset_enc: RESET mode switch (the standard
// T1 initial context state configuration).
func (m *MQC) ResetEnc() {
	m.ResetStates()
	m.SetState(ctxUNI, 0, 46)
	m.SetState(ctxAGG, 0, 3)
	m.SetState(ctxZC, 0, 4)
}

// RestartInitEnc is the port of opj_mqc_restart_init_enc: RESTART (TERMALL)
// reinitialisation. Normally called after at least one Flush().
func (m *MQC) RestartInitEnc() {
	// Figure C.10 - Initialization of the encoder (INITENC).
	m.a = 0x8000
	m.c = 0
	m.ct = 12
	m.bp--
	if m.buf[m.bp] == 0xff {
		m.ct = 13
	}
}

// ErtermEnc is the port of opj_mqc_erterm_enc: ERTERM (PTERM) mode switch.
func (m *MQC) ErtermEnc() {
	k := int32(11) - int32(m.ct) + 1

	for k > 0 {
		m.c <<= m.ct
		m.ct = 0
		m.byteout()
		k -= int32(m.ct)
	}

	if m.buf[m.bp] != 0xff {
		m.byteout()
	}
}

// SegmarkEnc is the port of opj_mqc_segmark_enc: SEGMARK (SEGSYM) mode switch,
// encoding the 4-symbol segmentation marker 1010 on context 18.
func (m *MQC) SegmarkEnc() {
	m.SetCurCtx(18)
	for i := uint32(1); i < 5; i++ {
		m.Encode(i % 2)
	}
}
