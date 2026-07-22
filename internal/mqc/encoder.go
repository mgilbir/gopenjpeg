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
