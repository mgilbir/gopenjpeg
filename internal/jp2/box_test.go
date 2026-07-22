package jp2

import (
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// --- byte builders ---------------------------------------------------------

// mkbox wraps a payload in an 8-byte box header of the given type.
func mkbox(typ uint32, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	cio.WriteBytes(b, uint32(8+len(payload)), 4)
	cio.WriteBytes(b[4:], typ, 4)
	copy(b[8:], payload)
	return b
}

// sigBox builds a valid signature box.
func sigBox() []byte {
	p := make([]byte, 4)
	cio.WriteBytes(p, jp2Magic, 4)
	return mkbox(boxJP, p)
}

// ftypBox builds a minimal File Type box (brand jp2, no compat list).
func ftypBox() []byte {
	p := make([]byte, 8)
	cio.WriteBytes(p, boxJP2, 4) // BR
	cio.WriteBytes(p[4:], 0, 4)  // MinV
	return mkbox(boxFTYP, p)
}

// ihdrPayload builds a 14-byte IHDR payload.
func ihdrPayload(h, w uint32, nc uint16, bpc, c, unk, ipr byte) []byte {
	p := make([]byte, 14)
	cio.WriteBytes(p, h, 4)
	cio.WriteBytes(p[4:], w, 4)
	cio.WriteBytes(p[8:], uint32(nc), 2)
	p[10], p[11], p[12], p[13] = bpc, c, unk, ipr
	return p
}

// captureManager records emitted error/warning/info messages.
type captureManager struct {
	mgr   *event.Manager
	errs  []string
	warns []string
	infos []string
}

func newCapture() *captureManager {
	c := &captureManager{}
	c.mgr = &event.Manager{
		ErrorHandler:   func(m string) { c.errs = append(c.errs, m) },
		WarningHandler: func(m string) { c.warns = append(c.warns, m) },
		InfoHandler:    func(m string) { c.infos = append(c.infos, m) },
	}
	return c
}

func (c *captureManager) hasErr(sub string) bool  { return anyContains(c.errs, sub) }
func (c *captureManager) hasWarn(sub string) bool { return anyContains(c.warns, sub) }

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// runProcedure feeds data through readHeaderProcedure with a fresh JP2/stub.
func runProcedure(data []byte) (*JP2, *captureManager, bool) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	stream := cio.NewMemoryInputStream(data)
	ok := jp2.readHeaderProcedure(stream, cap.mgr)
	return jp2, cap, ok
}

// --- well-formed baseline --------------------------------------------------

// TestValidMinimalHeader parses a minimal well-formed JP2 up to the codestream.
func TestValidMinimalHeader(t *testing.T) {
	jp2h := mkbox(boxJP2H, mkbox(boxIHDR, ihdrPayload(16, 32, 3, 7, 7, 0, 0)))
	data := concat(sigBox(), ftypBox(), jp2h, mkbox(boxJP2C, []byte{0x00}))
	jp2, cap, ok := runProcedure(data)
	if !ok {
		t.Fatalf("expected success, errs=%v", cap.errs)
	}
	if !jp2.hasJp2h || !jp2.hasIhdr {
		t.Fatalf("hasJp2h=%v hasIhdr=%v", jp2.hasJp2h, jp2.hasIhdr)
	}
	if jp2.jp2State&stateCodestream == 0 {
		t.Fatalf("codestream state not set")
	}
	if jp2.w != 32 || jp2.h != 16 || jp2.numcomps != 3 {
		t.Fatalf("bad ihdr parse: w=%d h=%d nc=%d", jp2.w, jp2.h, jp2.numcomps)
	}
}

// --- signature / ftyp ordering (hard errors) -------------------------------

// TestBadMagic: signature box with wrong magic number is a hard error
// (opj_jp2_read_jp: "bad magic number").
func TestBadMagic(t *testing.T) {
	p := make([]byte, 4)
	cio.WriteBytes(p, 0xdeadbeef, 4)
	data := concat(mkbox(boxJP, p))
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("bad magic number") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestSignatureWrongSize: a signature box whose payload is not 4 bytes errors
// (opj_jp2_read_jp: "Error with JP signature Box size"). jp2.c line ~2558.
func TestSignatureWrongSize(t *testing.T) {
	data := concat(mkbox(boxJP, []byte{0, 0, 0, 0, 0}))
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("JP signature Box size") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestFtypBeforeSignature: first box is ftyp; opj_jp2_read_ftyp requires state
// == SIGNATURE, so it errors ("second box in the file"). jp2.c line ~2599.
func TestFtypBeforeSignature(t *testing.T) {
	data := concat(ftypBox())
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("second box in the file") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// --- codestream placement --------------------------------------------------

// TestJp2cBeforeHeader: a jp2c box before any jp2h is "bad placed jpeg
// codestream". jp2.c line ~2296.
func TestJp2cBeforeHeader(t *testing.T) {
	data := concat(sigBox(), ftypBox(), mkbox(boxJP2C, []byte{0}))
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("bad placed jpeg codestream") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// --- box-length guards -----------------------------------------------------

// TestBoxLengthLessThanHeader: a box declaring length < 8 (here 4) is rejected
// with "invalid box size". jp2.c line ~2306.
func TestBoxLengthLessThanHeader(t *testing.T) {
	bad := make([]byte, 8)
	cio.WriteBytes(bad, 4, 4) // length 4 (< 8 header bytes)
	cio.WriteBytes(bad[4:], boxFTYP, 4)
	data := concat(sigBox(), bad)
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("invalid box size") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestXLBoxUndefinedSize: an XL box (length==1) whose 64-bit size low word is 0
// yields box.Length==0, caught as "Cannot handle box of undefined sizes".
// jp2.c line ~2300.
func TestXLBoxUndefinedSize(t *testing.T) {
	xl := make([]byte, 16)
	cio.WriteBytes(xl, 1, 4) // length marker: XL follows
	cio.WriteBytes(xl[4:], boxFTYP, 4)
	cio.WriteBytes(xl[8:], 0, 4)  // XL high word (must be 0)
	cio.WriteBytes(xl[12:], 0, 4) // XL low word == 0 -> undefined
	data := concat(sigBox(), xl)
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("undefined sizes") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestXLBoxTooLarge: an XL box with a non-zero high word is rejected as a box
// larger than 2^32. The guard lives in readBoxHdr, which returns false; as in C
// this ends the box loop so the procedure returns true, but the error is logged
// and (in the full ReadHeader flow) the missing jp2h then fails. jp2.c line ~527.
func TestXLBoxTooLarge(t *testing.T) {
	xl := make([]byte, 16)
	cio.WriteBytes(xl, 1, 4)
	cio.WriteBytes(xl[4:], boxFTYP, 4)
	cio.WriteBytes(xl[8:], 1, 4) // non-zero high word
	cio.WriteBytes(xl[12:], 0, 4)
	data := concat(sigBox(), xl)
	jp2, cap, _ := runProcedure(data)
	if !cap.hasErr("higher than 2^32") {
		t.Fatalf("errs=%v", cap.errs)
	}
	if jp2.hasJp2h {
		t.Fatal("no jp2h should have been read")
	}
}

// --- jp2h super-box guards -------------------------------------------------

// TestJp2hNoIhdr: a jp2h with no ihdr box errors ("no 'ihdr' box"). jp2.c
// line ~2754.
func TestJp2hNoIhdr(t *testing.T) {
	jp2h := mkbox(boxJP2H, mkbox(boxCOLR, colrEnum(1, 16)))
	data := concat(sigBox(), ftypBox(), jp2h)
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("no 'ihdr' box") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestJp2hInconsistentInnerLength: an inner box whose length exceeds the
// remaining jp2h payload is rejected ("box length is inconsistent"). jp2.c
// line ~2727.
func TestJp2hInconsistentInnerLength(t *testing.T) {
	inner := mkbox(boxIHDR, ihdrPayload(16, 32, 1, 7, 7, 0, 0))
	// Corrupt the inner box length to be larger than the payload.
	cio.WriteBytes(inner, 999999, 4)
	jp2h := mkbox(boxJP2H, inner)
	data := concat(sigBox(), ftypBox(), jp2h)
	_, cap, ok := runProcedure(data)
	if ok || !cap.hasErr("inconsistent") {
		t.Fatalf("ok=%v errs=%v", ok, cap.errs)
	}
}

// TestDuplicateIhdr: a second ihdr box (via a second jp2h) is ignored with a
// warning, not an error (opj_jp2_read_ihdr: "First ihdr box already read").
// jp2.c line ~572.
func TestDuplicateIhdr(t *testing.T) {
	jp2h := mkbox(boxJP2H, mkbox(boxIHDR, ihdrPayload(16, 32, 1, 7, 7, 0, 0)))
	data := concat(sigBox(), ftypBox(), jp2h, jp2h, mkbox(boxJP2C, []byte{0}))
	jp2, cap, ok := runProcedure(data)
	if !ok {
		t.Fatalf("expected success, errs=%v", cap.errs)
	}
	if !cap.hasWarn("First ihdr box already read") {
		t.Fatalf("expected duplicate-ihdr warning, warns=%v", cap.warns)
	}
	if jp2.w != 32 {
		t.Fatalf("first ihdr should win: w=%d", jp2.w)
	}
}

// --- individual box readers ------------------------------------------------

func TestIhdrBadSize(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	if jp2.readIhdr(make([]byte, 13), 13, cap.mgr) {
		t.Fatal("expected failure on 13-byte ihdr")
	}
	if !cap.hasErr("Bad image header box") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

func TestIhdrZeroDims(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	if jp2.readIhdr(ihdrPayload(0, 32, 1, 7, 7, 0, 0), 14, cap.mgr) {
		t.Fatal("expected failure on zero height")
	}
	if !cap.hasErr("Wrong values") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

func TestBpccBadSize(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	jp2.numcomps = 4
	jp2.bpc = 255
	jp2.comps = make([]Comps, 4)
	if jp2.readBpcc(make([]byte, 3), 3, cap.mgr) { // size != numcomps
		t.Fatal("expected failure")
	}
	if !cap.hasErr("Bad BPCC header box") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

func TestBpccWarnsWhenBpcNot255(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	jp2.numcomps = 2
	jp2.bpc = 7 // not 255
	jp2.comps = make([]Comps, 2)
	if !jp2.readBpcc([]byte{7, 7}, 2, cap.mgr) {
		t.Fatalf("expected success, errs=%v", cap.errs)
	}
	if !cap.hasWarn("BPCC header box is available") {
		t.Fatalf("expected warning, warns=%v", cap.warns)
	}
}

func TestColrBadSize(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	if jp2.readColr(make([]byte, 2), 2, cap.mgr) {
		t.Fatal("expected failure on <3-byte colr")
	}
	if !cap.hasErr("Bad COLR header box") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestColrMethGt2Ignored: an unsupported METH value causes the whole colr box
// to be ignored (info, not error) and jp2_has_colr stays 0. jp2.c line ~1586.
func TestColrMethGt2Ignored(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	body := []byte{254, 0, 0} // meth=254
	if !jp2.readColr(body, 3, cap.mgr) {
		t.Fatalf("expected success (ignored), errs=%v", cap.errs)
	}
	if jp2.color.JP2HasColr != 0 {
		t.Fatal("jp2_has_colr should remain 0 for ignored colr")
	}
}

// TestColrSecondIgnored: a second colr box is ignored with an info message and
// does not overwrite the first. jp2.c line ~1485.
func TestColrSecondIgnored(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	if !jp2.readColr(colrEnum(1, 16), 7, cap.mgr) {
		t.Fatalf("first colr failed: %v", cap.errs)
	}
	if !jp2.readColr(colrEnum(1, 17), 7, cap.mgr) {
		t.Fatalf("second colr should be accepted-and-ignored")
	}
	if jp2.enumcs != 16 {
		t.Fatalf("second colr must not overwrite: enumcs=%d", jp2.enumcs)
	}
}

func TestPclrInvalidEntries(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	// NE=0
	body := []byte{0x00, 0x00, 0x01}
	if jp2.readPclr(body, uint32(len(body)), cap.mgr) {
		t.Fatal("expected failure on 0 entries")
	}
	if !cap.hasErr("Reports 0 entries") && !cap.hasErr("Reports 0") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

func TestPclrZeroChannels(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	body := []byte{0x00, 0x02, 0x00} // NE=2, NPC=0
	if jp2.readPclr(body, uint32(len(body)), cap.mgr) {
		t.Fatal("expected failure on 0 channels")
	}
	if !cap.hasErr("0 palette columns") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestCmapWithoutPclr: a cmap box before any pclr errors. jp2.c line ~1284.
func TestCmapWithoutPclr(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	if jp2.readCmap(make([]byte, 4), 4, cap.mgr) {
		t.Fatal("expected failure: cmap before pclr")
	}
	if !cap.hasErr("Need to read a PCLR box before the CMAP box") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestDuplicateCmap: a second cmap box is rejected ("Only one CMAP box is
// allowed"). jp2.c line ~1293.
func TestDuplicateCmap(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	jp2.color.Pclr = &Pclr{NrChannels: 1}
	cmapBody := []byte{0, 0, 0, 0} // cmp=0 mtyp=0 pcol=0
	if !jp2.readCmap(cmapBody, 4, cap.mgr) {
		t.Fatalf("first cmap failed: %v", cap.errs)
	}
	if jp2.readCmap(cmapBody, 4, cap.mgr) {
		t.Fatal("expected failure on second cmap")
	}
	if !cap.hasErr("Only one CMAP box is allowed") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

func TestCdefZeroN(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	body := []byte{0x00, 0x00} // N=0
	if jp2.readCdef(body, 2, cap.mgr) {
		t.Fatal("expected failure on N=0")
	}
	if !cap.hasErr("equal to zero in CDEF box") {
		t.Fatalf("errs=%v", cap.errs)
	}
}

// TestDuplicateCdef: a second cdef box is rejected (opj_jp2_read_cdef returns
// FALSE when jp2->color.jp2_cdef is already set). jp2.c line ~1410.
func TestDuplicateCdef(t *testing.T) {
	jp2, _ := newTestJP2()
	cap := newCapture()
	body := concat([]byte{0x00, 0x01}, []byte{0, 0, 0, 0, 0, 0}) // N=1, one entry
	if !jp2.readCdef(body, uint32(len(body)), cap.mgr) {
		t.Fatalf("first cdef failed: %v", cap.errs)
	}
	if jp2.readCdef(body, uint32(len(body)), cap.mgr) {
		t.Fatal("expected failure on duplicate cdef")
	}
}

// colrEnum builds a meth==1 enumerated colr payload of size 7.
func colrEnum(meth byte, enumcs uint32) []byte {
	p := make([]byte, 7)
	p[0] = meth
	p[1] = 0 // precedence
	p[2] = 0 // approx
	cio.WriteBytes(p[3:], enumcs, 4)
	return p
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
