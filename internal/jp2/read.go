package jp2

import (
	"errors"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// ErrRead is returned by ReadHeader when box parsing or the codestream header
// read fails. The specific diagnostic is delivered through the event.Manager,
// mirroring the C convention where opj_jp2_* returns OPJ_FALSE and the reason is
// reported via opj_event_msg.
var ErrRead = errors.New("jp2: malformed JP2 file")

// boxHandler ports the opj_jp2_header_handler_t.handler function-pointer type:
// a box reader taking the box payload and its size.
type boxHandler func(jp2 *JP2, data []byte, size uint32, mgr *event.Manager) bool

// findHandler ports opj_jp2_find_handler over the jp2_header[] table
// {JP, FTYP, JP2H}. It returns nil when no handler matches.
func findHandler(id uint32) boxHandler {
	switch id {
	case boxJP:
		return (*JP2).readJp
	case boxFTYP:
		return (*JP2).readFtyp
	case boxJP2H:
		return (*JP2).readJp2h
	}
	return nil
}

// imgFindHandler ports opj_jp2_img_find_handler over the jp2_img_header[] table
// {IHDR, COLR, BPCC, PCLR, CMAP, CDEF}. It returns nil when no handler matches.
func imgFindHandler(id uint32) boxHandler {
	switch id {
	case boxIHDR:
		return (*JP2).readIhdr
	case boxCOLR:
		return (*JP2).readColr
	case boxBPCC:
		return (*JP2).readBpcc
	case boxPCLR:
		return (*JP2).readPclr
	case boxCMAP:
		return (*JP2).readCmap
	case boxCDEF:
		return (*JP2).readCdef
	}
	return nil
}

// readJp ports opj_jp2_read_jp: validate the signature box.
func (jp2 *JP2) readJp(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.jp2State != stateNone {
		mgr.Errorf("The signature box must be the first box in the file.\n")
		return false
	}
	if size != 4 {
		mgr.Errorf("Error with JP signature Box size\n")
		return false
	}
	magic := cio.ReadBytes(data[0:], 4)
	if magic != jp2Magic {
		mgr.Errorf("Error with JP Signature : bad magic number\n")
		return false
	}
	jp2.jp2State |= stateSignature
	return true
}

// readFtyp ports opj_jp2_read_ftyp: parse the File Type box (brand, minor
// version and the compatibility list).
func (jp2 *JP2) readFtyp(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.jp2State != stateSignature {
		mgr.Errorf("The ftyp box must be the second box in the file.\n")
		return false
	}
	if size < 8 {
		mgr.Errorf("Error with FTYP signature Box size\n")
		return false
	}

	jp2.brand = cio.ReadBytes(data[0:], 4)      // BR
	jp2.minversion = cio.ReadBytes(data[4:], 4) // MinV
	data = data[8:]

	remaining := size - 8
	// the number of remaining bytes should be a multiple of 4
	if remaining&0x3 != 0 {
		mgr.Errorf("Error with FTYP signature Box size\n")
		return false
	}

	jp2.numcl = remaining >> 2
	if jp2.numcl != 0 {
		jp2.cl = make([]uint32, jp2.numcl)
	}
	for i := uint32(0); i < jp2.numcl; i++ {
		jp2.cl[i] = cio.ReadBytes(data[4*i:], 4) // CLi
	}

	jp2.jp2State |= stateFileType
	return true
}

// readJp2h ports opj_jp2_read_jp2h: walk the JP2 Header super-box, dispatching
// each contained box through the image-header handler table. It requires an
// ihdr box to be present.
func (jp2 *JP2) readJp2h(data []byte, size uint32, mgr *event.Manager) bool {
	if jp2.jp2State&stateFileType != stateFileType {
		mgr.Errorf("The  box must be the first box in the file.\n")
		return false
	}

	jp2.jp2ImgState = imgStateNone
	hasIhdr := false

	for size > 0 {
		box, boxHdrSize, ok := readBoxHdrChar(data, size, mgr)
		if !ok {
			mgr.Errorf("Stream error while reading JP2 Header box\n")
			return false
		}

		if box.Length > size {
			mgr.Errorf("Stream error while reading JP2 Header box: box length is inconsistent.\n")
			return false
		}

		handler := imgFindHandler(box.Type)
		currentDataSize := box.Length - boxHdrSize
		payload := data[boxHdrSize:]

		if handler != nil {
			if !handler(jp2, payload, currentDataSize, mgr) {
				return false
			}
		} else {
			jp2.jp2ImgState |= imgStateUnknown
		}

		if box.Type == boxIHDR {
			hasIhdr = true
		}

		data = data[box.Length:]
		size -= box.Length
	}

	if !hasIhdr {
		mgr.Errorf("Stream error while reading JP2 Header box: no 'ihdr' box.\n")
		return false
	}

	jp2.jp2State |= stateHeader
	jp2.hasJp2h = true
	return true
}

// readHeaderProcedure ports opj_jp2_read_header_procedure: the top-level box
// loop over the whole file up to the codestream box. It reproduces every guard
// (undefined/inconsistent box sizes, misplaced boxes, insufficient stream
// bytes) and the warning-vs-error tolerances of the C code, including the
// "already read codestream" leniency for a trailing truncated box.
func (jp2 *JP2) readHeaderProcedure(stream *cio.Stream, mgr *event.Manager) bool {
	for {
		box, nbBytesRead, ok := jp2.readBoxHdr(stream, mgr)
		if !ok {
			break
		}

		// is it the codestream box?
		if box.Type == boxJP2C {
			if jp2.jp2State&stateHeader != 0 {
				jp2.jp2State |= stateCodestream
				return true
			}
			mgr.Errorf("bad placed jpeg codestream\n")
			return false
		} else if box.Length == 0 {
			mgr.Errorf("Cannot handle box of undefined sizes\n")
			return false
		} else if box.Length < nbBytesRead {
			// testcase 1851.pdf.SIGSEGV.ce9.948
			mgr.Errorf("invalid box size %d (%x)\n", box.Length, box.Type)
			return false
		}

		handler := findHandler(box.Type)
		handlerMisplaced := imgFindHandler(box.Type)
		currentDataSize := box.Length - nbBytesRead

		if handler != nil || handlerMisplaced != nil {
			if handler == nil {
				mgr.Warnf("Found a misplaced '%s' box outside jp2h box\n", boxName(box.Type))
				if jp2.jp2State&stateHeader != 0 {
					// read anyway, we already have jp2h
					handler = handlerMisplaced
				} else {
					mgr.Warnf("JPEG2000 Header box not read yet, '%s' box will be ignored\n", boxName(box.Type))
					jp2.jp2State |= stateUnknown
					if n, _ := stream.Skip(int64(currentDataSize), mgr); n != int64(currentDataSize) {
						mgr.Errorf("Problem with skipping JPEG2000 box, stream error\n")
						return false
					}
					continue
				}
			}
			if int64(currentDataSize) > stream.NumberByteLeft() {
				// do not even try to allocate if we can't read
				mgr.Errorf("Invalid box size %d for box '%s'. Need %d bytes, %d bytes remaining \n",
					box.Length, boxName(box.Type), currentDataSize, uint32(stream.NumberByteLeft()))
				return false
			}

			buf := make([]byte, currentDataSize)
			n, _ := stream.Read(buf, mgr)
			if uint32(n) != currentDataSize {
				mgr.Errorf("Problem with reading JPEG2000 box, stream error\n")
				return false
			}

			if !handler(jp2, buf, currentDataSize, mgr) {
				return false
			}
		} else {
			if jp2.jp2State&stateSignature == 0 {
				mgr.Errorf("Malformed JP2 file format: first box must be JPEG 2000 signature box\n")
				return false
			}
			if jp2.jp2State&stateFileType == 0 {
				mgr.Errorf("Malformed JP2 file format: second box must be file type box\n")
				return false
			}
			jp2.jp2State |= stateUnknown
			if n, _ := stream.Skip(int64(currentDataSize), mgr); n != int64(currentDataSize) {
				if jp2.jp2State&stateCodestream != 0 {
					// If we already read the codestream, do not error out.
					// Needed for data/input/nonregression/issue254.jp2
					mgr.Warnf("Problem with skipping JPEG2000 box, stream error\n")
					return true
				}
				mgr.Errorf("Problem with skipping JPEG2000 box, stream error\n")
				return false
			}
		}
	}

	return true
}

// boxName renders a 4-byte box type as its character signature for diagnostics,
// mirroring the "%c%c%c%c" formatting in jp2.c.
func boxName(t uint32) string {
	return string([]byte{byte(t >> 24), byte(t >> 16), byte(t >> 8), byte(t)})
}

// ReadHeader ports opj_jp2_read_header. It validates the decoder state, runs the
// box-reading procedure (opj_jp2_read_header_procedure), enforces the mandatory
// jp2h/ihdr boxes, delegates the codestream main-header read to the codec, and
// finally maps the enumerated colour space onto the image and transfers any
// captured ICC profile. It returns the decoded image.
//
// The C validation and procedure lists (opj_jp2_setup_decoding_validation, which
// is empty, and opj_jp2_setup_header_reading) are expressed here as the direct
// ordered calls they represent.
func (jp2 *JP2) ReadHeader(stream *cio.Stream, mgr *event.Manager) (*image.Image, error) {
	// setup_decoding_validation adds no procedures; nothing to validate.

	// header reading procedure
	if !jp2.readHeaderProcedure(stream, mgr) {
		return nil, ErrRead
	}
	if !jp2.hasJp2h {
		mgr.Errorf("JP2H box missing. Required.\n")
		return nil, ErrRead
	}
	if !jp2.hasIhdr {
		mgr.Errorf("IHDR box_missing. Required.\n")
		return nil, ErrRead
	}

	img, err := jp2.codec.ReadHeader(stream, mgr)
	if err != nil {
		return nil, err
	}

	if img != nil {
		// Set image colour space from the enumerated value.
		switch jp2.enumcs {
		case 16:
			img.ColorSpace = image.ClrspcSRGB
		case 17:
			img.ColorSpace = image.ClrspcGray
		case 18:
			img.ColorSpace = image.ClrspcSYCC
		case 24:
			img.ColorSpace = image.ClrspcEYCC
		case 12:
			img.ColorSpace = image.ClrspcCMYK
		default:
			img.ColorSpace = image.ClrspcUnknown
		}

		if jp2.color.ICCProfileBuf != nil {
			img.ICCProfileBuf = jp2.color.ICCProfileBuf
			img.ICCProfileLen = jp2.color.ICCProfileLen
			jp2.color.ICCProfileBuf = nil
		}
	}

	return img, nil
}
