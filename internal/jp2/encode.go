package jp2

import (
	"errors"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ErrEncode is returned by the encode-side methods when validation or a stream
// write fails; the specific diagnostic is delivered via the event.Manager.
var ErrEncode = errors.New("jp2: encode failure")

// writeIhdr ports opj_jp2_write_ihdr: build the 22-byte Image Header box.
func (jp2 *JP2) writeIhdr() []byte {
	d := make([]byte, 22)
	cio.WriteBytes(d[0:], 22, 4)            // box size
	cio.WriteBytes(d[4:], boxIHDR, 4)       // IHDR
	cio.WriteBytes(d[8:], jp2.h, 4)         // HEIGHT
	cio.WriteBytes(d[12:], jp2.w, 4)        // WIDTH
	cio.WriteBytes(d[16:], jp2.numcomps, 2) // NC
	cio.WriteBytes(d[18:], jp2.bpc, 1)      // BPC
	cio.WriteBytes(d[19:], jp2.c, 1)        // C (always 7)
	cio.WriteBytes(d[20:], jp2.unkC, 1)     // UnkC
	cio.WriteBytes(d[21:], jp2.ipr, 1)      // IPR
	return d
}

// writeBpcc ports opj_jp2_write_bpcc: build the Bits Per Component box.
func (jp2 *JP2) writeBpcc() []byte {
	size := 8 + jp2.numcomps
	d := make([]byte, size)
	cio.WriteBytes(d[0:], size, 4)    // box size
	cio.WriteBytes(d[4:], boxBPCC, 4) // BPCC
	for i := uint32(0); i < jp2.numcomps; i++ {
		cio.WriteBytes(d[8+i:], jp2.comps[i].Bpcc, 1)
	}
	return d
}

// writeCdef ports opj_jp2_write_cdef: build the Channel Definition box.
func (jp2 *JP2) writeCdef() []byte {
	n := uint32(jp2.color.Cdef.N)
	size := 10 + 6*n
	d := make([]byte, size)
	cio.WriteBytes(d[0:], size, 4)                     // box size
	cio.WriteBytes(d[4:], boxCDEF, 4)                  // CDEF
	cio.WriteBytes(d[8:], uint32(jp2.color.Cdef.N), 2) // N
	off := uint32(10)
	for i := uint16(0); i < jp2.color.Cdef.N; i++ {
		cio.WriteBytes(d[off:], uint32(jp2.color.Cdef.Info[i].Cn), 2)     // Cni
		cio.WriteBytes(d[off+2:], uint32(jp2.color.Cdef.Info[i].Typ), 2)  // Typi
		cio.WriteBytes(d[off+4:], uint32(jp2.color.Cdef.Info[i].Asoc), 2) // Asoci
		off += 6
	}
	return d
}

// writeColr ports opj_jp2_write_colr: build the Colour Specification box for the
// enumerated (meth==1) or ICC (meth==2) case. It returns nil for an unexpected
// meth value, matching the C NULL return.
func (jp2 *JP2) writeColr() []byte {
	size := uint32(11)
	switch jp2.meth {
	case 1:
		size += 4 // EnumCS
	case 2:
		size += jp2.color.ICCProfileLen // ICC profile
	default:
		return nil
	}

	d := make([]byte, size)
	cio.WriteBytes(d[0:], size, 4)           // box size
	cio.WriteBytes(d[4:], boxCOLR, 4)        // COLR
	cio.WriteBytes(d[8:], jp2.meth, 1)       // METH
	cio.WriteBytes(d[9:], jp2.precedence, 1) // PRECEDENCE
	cio.WriteBytes(d[10:], jp2.approx, 1)    // APPROX

	if jp2.meth == 1 {
		cio.WriteBytes(d[11:], jp2.enumcs, 4) // EnumCS
	} else { // meth == 2
		copy(d[11:], jp2.color.ICCProfileBuf[:jp2.color.ICCProfileLen])
	}
	return d
}

// writeJp2h ports opj_jp2_write_jp2h: assemble and stream out the JP2 Header
// super-box (ihdr, optional bpcc, colr, optional cdef).
func (jp2 *JP2) writeJp2h(stream *cio.Stream, mgr *event.Manager) error {
	// Choose the writer set exactly as C does (bpcc only when bpc==255).
	var parts [][]byte
	if jp2.bpc == 255 {
		parts = append(parts, jp2.writeIhdr(), jp2.writeBpcc(), jp2.writeColr())
	} else {
		parts = append(parts, jp2.writeIhdr(), jp2.writeColr())
	}
	if jp2.color.Cdef != nil {
		parts = append(parts, jp2.writeCdef())
	}

	jp2hSize := uint32(8)
	for _, p := range parts {
		if p == nil {
			mgr.Errorf("Not enough memory to hold JP2 Header data\n")
			return ErrEncode
		}
		jp2hSize += uint32(len(p))
	}

	var hdr [8]byte
	cio.WriteBytes(hdr[0:], jp2hSize, 4)
	cio.WriteBytes(hdr[4:], boxJP2H, 4)
	if n, _ := stream.Write(hdr[:], mgr); n != 8 {
		mgr.Errorf("Stream error while writing JP2 Header box\n")
		return ErrEncode
	}
	for _, p := range parts {
		if n, _ := stream.Write(p, mgr); n != len(p) {
			mgr.Errorf("Stream error while writing JP2 Header box\n")
			return ErrEncode
		}
	}
	return nil
}

// writeFtyp ports opj_jp2_write_ftyp: write the File Type box.
func (jp2 *JP2) writeFtyp(stream *cio.Stream, mgr *event.Manager) error {
	size := 16 + 4*jp2.numcl
	d := make([]byte, size)
	cio.WriteBytes(d[0:], size, 4)            // box size
	cio.WriteBytes(d[4:], boxFTYP, 4)         // FTYP
	cio.WriteBytes(d[8:], jp2.brand, 4)       // BR
	cio.WriteBytes(d[12:], jp2.minversion, 4) // MinV
	for i := uint32(0); i < jp2.numcl; i++ {
		cio.WriteBytes(d[16+4*i:], jp2.cl[i], 4) // CLi
	}
	if n, _ := stream.Write(d, mgr); uint32(n) != size {
		mgr.Errorf("Error while writing ftyp data to stream\n")
		return ErrEncode
	}
	return nil
}

// writeJp ports opj_jp2_write_jp: write the 12-byte signature box.
func (jp2 *JP2) writeJp(stream *cio.Stream, mgr *event.Manager) error {
	var d [12]byte
	cio.WriteBytes(d[0:], 12, 4)       // box length
	cio.WriteBytes(d[4:], boxJP, 4)    // box type
	cio.WriteBytes(d[8:], jp2Magic, 4) // magic number
	if n, _ := stream.Write(d[:], mgr); n != 12 {
		return ErrEncode
	}
	return nil
}

// skipJp2c ports opj_jp2_skip_jp2c: record the offset where the jp2c box header
// will go, then skip 8 bytes so the codestream can be written; writeJp2c later
// back-patches the length. Requires a seekable stream.
func (jp2 *JP2) skipJp2c(stream *cio.Stream, mgr *event.Manager) error {
	jp2.j2kCodestreamOffset = stream.Tell()
	if n, _ := stream.Skip(8, mgr); n != 8 {
		return ErrEncode
	}
	return nil
}

// writeJp2c ports opj_jp2_write_jp2c: back-patch the jp2c box header with the
// codestream length. It must be called after the codestream is written and
// requires a seekable stream (as C asserts).
func (jp2 *JP2) writeJp2c(stream *cio.Stream, mgr *event.Manager) error {
	if !stream.HasSeek() {
		mgr.Errorf("Failed to seek in the stream.\n")
		return ErrEncode
	}
	j2kCodestreamExit := stream.Tell()

	var d [8]byte
	cio.WriteBytes(d[0:], uint32(j2kCodestreamExit-jp2.j2kCodestreamOffset), 4) // size of codestream
	cio.WriteBytes(d[4:], boxJP2C, 4)                                           // JP2C

	if err := stream.SeekTo(jp2.j2kCodestreamOffset, mgr); err != nil {
		mgr.Errorf("Failed to seek in the stream.\n")
		return ErrEncode
	}
	if n, _ := stream.Write(d[:], mgr); n != 8 {
		mgr.Errorf("Failed to seek in the stream.\n")
		return ErrEncode
	}
	if err := stream.SeekTo(j2kCodestreamExit, mgr); err != nil {
		mgr.Errorf("Failed to seek in the stream.\n")
		return ErrEncode
	}
	return nil
}

// defaultValidation ports opj_jp2_default_validation: validate codec state,
// pointers and parameters, and that the stream supports seeking.
func (jp2 *JP2) defaultValidation(stream *cio.Stream) bool {
	valid := jp2.jp2State == stateNone
	valid = valid && jp2.jp2ImgState == imgStateNone
	valid = valid && jp2.codec != nil

	valid = valid && jp2.numcl > 0
	valid = valid && jp2.h > 0
	valid = valid && jp2.w > 0
	for i := uint32(0); i < jp2.numcomps; i++ {
		valid = valid && (jp2.comps[i].Bpcc&0x7f) < 38 // 0 is valid, ignore sign
	}
	valid = valid && (jp2.meth > 0 && jp2.meth < 3)
	valid = valid && stream.HasSeek()

	return valid
}

// SetupEncoder ports opj_jp2_setup_encoder: derive all JP2-box parameters from
// the image and user parameters, forwarding codestream setup to the codec. It
// mirrors every parameter check and the automatic cdef/colr decisions of C.
func (jp2 *JP2) SetupEncoder(params *EncoderParams, img *image.Image, mgr *event.Manager) error {
	if params == nil || img == nil {
		return ErrEncode
	}

	// Check number of components against the standard.
	if img.Numcomps < 1 || img.Numcomps > 16384 {
		mgr.Errorf("Invalid number of components specified while setting up JP2 encoder\n")
		return ErrEncode
	}

	if err := jp2.codec.SetupEncoder(params, img, mgr); err != nil {
		return err
	}

	// Profile box.
	jp2.brand = boxJP2
	jp2.minversion = 0
	jp2.numcl = 1
	jp2.cl = []uint32{boxJP2}

	// Image Header box.
	jp2.numcomps = img.Numcomps
	jp2.comps = make([]Comps, jp2.numcomps)

	jp2.h = img.Y1 - img.Y0
	jp2.w = img.X1 - img.X0

	// BPC.
	depth0 := img.Comps[0].Prec - 1
	sign := img.Comps[0].Sgnd
	jp2.bpc = depth0 + (sign << 7)
	for i := uint32(1); i < img.Numcomps; i++ {
		depth := img.Comps[i].Prec - 1
		if depth0 != depth {
			jp2.bpc = 255
		}
	}
	jp2.c = 7
	jp2.unkC = 0
	jp2.ipr = 0

	// BitsPerComponent box.
	for i := uint32(0); i < img.Numcomps; i++ {
		jp2.comps[i].Bpcc = img.Comps[i].Prec - 1 + (img.Comps[i].Sgnd << 7)
	}

	// Colour Specification box.
	if img.ICCProfileLen != 0 {
		jp2.meth = 2
		jp2.enumcs = 0
		jp2.color.ICCProfileBuf = make([]byte, img.ICCProfileLen)
		jp2.color.ICCProfileLen = img.ICCProfileLen
		copy(jp2.color.ICCProfileBuf, img.ICCProfileBuf[:img.ICCProfileLen])
	} else {
		jp2.meth = 1
		switch img.ColorSpace {
		case image.ClrspcSRGB:
			jp2.enumcs = 16 // sRGB (IEC 61966-2-1)
		case image.ClrspcGray:
			jp2.enumcs = 17
		case image.ClrspcSYCC:
			jp2.enumcs = 18 // YUV
		case image.ClrspcEYCC:
			jp2.enumcs = 24
		case image.ClrspcCMYK:
			jp2.enumcs = 12
		}
	}

	// Channel Definition box (best-effort automatic creation for a single alpha).
	alphaCount := uint32(0)
	alphaChannel := uint32(0)
	for i := uint32(0); i < img.Numcomps; i++ {
		if img.Comps[i].Alpha != 0 {
			alphaCount++
			alphaChannel = i
		}
	}
	colorChannels := uint32(0)
	if alphaCount == 1 { // no way to deal with more than 1 alpha channel
		switch jp2.enumcs {
		case 16, 18:
			colorChannels = 3
		case 17:
			colorChannels = 1
		default:
			alphaCount = 0
		}
		if alphaCount == 0 {
			mgr.Warnf("Alpha channel specified but unknown enumcs. No cdef box will be created.\n")
		} else if img.Numcomps < colorChannels+1 {
			mgr.Warnf("Alpha channel specified but not enough image components for an automatic cdef box creation.\n")
			alphaCount = 0
		} else if alphaChannel < colorChannels {
			mgr.Warnf("Alpha channel position conflicts with color channel. No cdef box will be created.\n")
			alphaCount = 0
		}
	} else if alphaCount > 1 {
		mgr.Warnf("Multiple alpha channels specified. No cdef box will be created.\n")
	}
	if alphaCount == 1 { // if here, we know what we can do
		cdef := &Cdef{
			Info: make([]CdefInfo, img.Numcomps),
			N:    uint16(img.Numcomps),
		}
		jp2.color.Cdef = cdef
		var i uint32
		for i = 0; i < colorChannels; i++ {
			cdef.Info[i].Cn = uint16(i)
			cdef.Info[i].Typ = 0
			cdef.Info[i].Asoc = uint16(i + 1)
		}
		for ; i < img.Numcomps; i++ {
			if img.Comps[i].Alpha != 0 { // exactly once
				cdef.Info[i].Cn = uint16(i)
				cdef.Info[i].Typ = 1 // opacity channel
				cdef.Info[i].Asoc = 0
			} else {
				cdef.Info[i].Cn = uint16(i)
				cdef.Info[i].Typ = 65535
				cdef.Info[i].Asoc = 65535
			}
		}
	}

	jp2.precedence = 0
	jp2.approx = 0
	jp2.jpipOn = params.JpipOn

	return nil
}

// StartCompress ports opj_jp2_start_compress: validate parameters
// (opj_jp2_default_validation), write the JP/FTYP/JP2H boxes and reserve the
// jp2c box header, then start the codestream compression.
//
// The C validation and header-writing procedure lists are inlined here as the
// ordered calls they contain (opj_jp2_setup_encoding_validation ->
// [defaultValidation]; opj_jp2_setup_header_writing ->
// [writeJp, writeFtyp, writeJp2h, (jpip skipIptr), skipJp2c]).
func (jp2 *JP2) StartCompress(stream *cio.Stream, img *image.Image, mgr *event.Manager) error {
	// validation
	if !jp2.defaultValidation(stream) {
		return ErrEncode
	}

	// header writing
	if err := jp2.writeJp(stream, mgr); err != nil {
		return err
	}
	if err := jp2.writeFtyp(stream, mgr); err != nil {
		return err
	}
	if err := jp2.writeJp2h(stream, mgr); err != nil {
		return err
	}
	// NOTE: JPIP iptr reservation (opj_jpip_skip_iptr) is only compiled with
	// USE_JPIP in C; it is intentionally omitted here (jpipOn is retained for
	// parameter parity).
	if err := jp2.skipJp2c(stream, mgr); err != nil {
		return err
	}

	return jp2.codec.StartCompress(stream, img, mgr)
}

// Encode ports opj_jp2_encode: forwarded to the codec.
func (jp2 *JP2) Encode(stream *cio.Stream, mgr *event.Manager) error {
	return jp2.codec.Encode(stream, mgr)
}

// EndCompress ports opj_jp2_end_compress: end the codestream compression, then
// back-patch the jp2c box length (opj_jp2_setup_end_header_writing ->
// [writeJp2c]).
func (jp2 *JP2) EndCompress(stream *cio.Stream, mgr *event.Manager) error {
	if err := jp2.codec.EndCompress(stream, mgr); err != nil {
		return err
	}
	return jp2.writeJp2c(stream, mgr)
}

// WriteTile ports opj_jp2_write_tile: forwarded to the codec.
func (jp2 *JP2) WriteTile(tileIndex uint32, data []byte, stream *cio.Stream, mgr *event.Manager) error {
	return jp2.codec.WriteTile(tileIndex, data, stream, mgr)
}

// EncoderSetExtraOptions ports opj_jp2_encoder_set_extra_options: forwarded to
// the codec.
func (jp2 *JP2) EncoderSetExtraOptions(options []string, mgr *event.Manager) error {
	return jp2.codec.EncoderSetExtraOptions(options, mgr)
}
