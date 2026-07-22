package mqc

// Constants mirroring the C reference.
const (
	// numCtxs is MQC_NUMCTXS (mqc.h).
	numCtxs = 19
	// cblkDataExtra is OPJ_COMMON_CBLK_DATA_EXTRA (opj_common.h): the margin
	// of writable bytes past the input that the decoder temporarily
	// overwrites with a fake 0xFF 0xFF marker.
	cblkDataExtra = 2
	// bypassCTInit is BYPASS_CT_INIT (mqc.h): a sentinel > 8 flagging that
	// opj_mqc_bypass_enc() has not yet been called.
	bypassCTInit = 0xDEADBEEF
)

// T1 context numbers (t1.h). Only the ones referenced by the MQ coder's
// reset helpers are needed here.
const (
	ctxZC  = 0  // T1_CTXNO_ZC
	ctxAGG = 17 // T1_CTXNO_AGG
	ctxUNI = 18 // T1_CTXNO_UNI
)

// MQC is the Go equivalent of opj_mqc_t: the state of an MQ (or RAW) coder.
//
// It is used for both encoding and decoding. For encoding the coder owns a
// growable output buffer (buf); for decoding buf is the caller-provided input
// buffer, which must have cblkDataExtra (=2) writable scratch bytes past the
// coded length (see InitDec / RawInitDec).
//
// bp, start and end are integer offsets into buf, replacing the C pointer
// arithmetic. curctx is an index into ctxs (the C code uses a pointer into the
// ctxs array); ctxs entries are indices into the states table.
type MQC struct {
	// c is the temporary buffer where bits are coded/decoded.
	c uint32
	// a is the interval register (decoder and encoder).
	a uint32
	// ct is the number of bits already read or free to write.
	ct uint32
	// endOfByteStreamCounter counts how many times a terminating
	// 0xFF >0x8F marker has been read (decoder only).
	endOfByteStreamCounter uint32

	// bp is the current position in buf.
	bp int
	// start is the start of the coded data in buf.
	start int
	// end is the end of the coded data in buf (decoder only).
	end int
	// buf is the byte buffer (owned when encoding, caller-provided when
	// decoding).
	buf []byte

	// ctxs holds, for each of the 19 contexts, the index of its current state
	// in the states table.
	ctxs [numCtxs]int32
	// curctx is the index (into ctxs) of the active context.
	curctx int

	// backup holds the original values of the 2 bytes at end[0] and end[1],
	// temporarily overwritten by the decoder's fake marker.
	backup [cblkDataExtra]byte
}

// NumBytes is the port of opj_mqc_numbytes: the number of bytes written/read
// since initialisation.
func (m *MQC) NumBytes() uint32 {
	return uint32(m.bp - m.start)
}

// ResetStates is the port of opj_mqc_resetstates: reset every context to the
// initial (roughly equiprobable) state.
func (m *MQC) ResetStates() {
	for i := range m.ctxs {
		m.ctxs[i] = 0
	}
}

// SetState is the port of opj_mqc_setstate: set the state of context ctxno to
// the state selected by (msb, prob): index = msb + (prob << 1).
func (m *MQC) SetState(ctxno, msb uint32, prob int32) {
	m.ctxs[ctxno] = int32(msb) + (prob << 1)
}

// SetCurCtx is the port of the opj_mqc_setcurctx macro: select the active
// context.
func (m *MQC) SetCurCtx(ctxno uint32) {
	m.curctx = int(ctxno)
}

// EndOfByteStreamCounter exposes the decoder's terminating-marker counter,
// mirroring opj_mqc_t.end_of_byte_stream_counter.
func (m *MQC) EndOfByteStreamCounter() uint32 {
	return m.endOfByteStreamCounter
}
