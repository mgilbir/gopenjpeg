package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// Box ports opj_jp2_box_t: a parsed box header (length includes the header
// bytes; type is the 4-character box signature).
type Box struct {
	Length  uint32 // total box length in bytes, including the header
	Type    uint32 // box type signature
	InitPos int32  // init_pos (unused in the read path; kept for parity)
}

// readBoxHdr ports opj_jp2_read_boxhdr: read an 8-byte box header from the
// stream, handling the "last box" (length 0) and XL (length 1, 64-bit) forms
// with the same overflow guards as C. It returns the parsed box, the number of
// header bytes consumed, and false when the header could not be read in full
// (end of stream) or an unrecoverable size was encountered.
func (jp2 *JP2) readBoxHdr(stream *cio.Stream, mgr *event.Manager) (box Box, nbBytesRead uint32, ok bool) {
	var dataHeader [8]byte

	n, _ := stream.Read(dataHeader[:], mgr)
	nbBytesRead = uint32(n)
	if nbBytesRead != 8 {
		return box, nbBytesRead, false
	}

	// process read data
	box.Length = cio.ReadBytes(dataHeader[0:], 4)
	box.Type = cio.ReadBytes(dataHeader[4:], 4)

	if box.Length == 0 { // last box: extends to end of stream
		bleft := stream.NumberByteLeft()
		if bleft > int64(0xFFFFFFFF-8) {
			mgr.Errorf("Cannot handle box sizes higher than 2^32\n")
			return box, nbBytesRead, false
		}
		box.Length = uint32(bleft) + 8
		return box, nbBytesRead, true
	}

	// "special very large box": length == 1 means the real 64-bit size follows.
	if box.Length == 1 {
		m, _ := stream.Read(dataHeader[:], mgr)
		nbRead2 := uint32(m)
		if nbRead2 != 8 {
			if nbRead2 > 0 {
				nbBytesRead += nbRead2
			}
			return box, nbBytesRead, false
		}
		nbBytesRead = 16
		xlPartSize := cio.ReadBytes(dataHeader[0:], 4)
		if xlPartSize != 0 {
			mgr.Errorf("Cannot handle box sizes higher than 2^32\n")
			return box, nbBytesRead, false
		}
		box.Length = cio.ReadBytes(dataHeader[4:], 4)
	}
	return box, nbBytesRead, true
}

// readBoxHdrChar ports opj_jp2_read_boxhdr_char: parse a box header from an
// in-memory buffer (used when walking the jp2h super-box payload). It bounds
// every read against boxMaxSize and applies the XL and undefined-length guards.
// It returns the parsed box, the header size consumed, and false on error
// (message emitted via the manager, exactly as C).
func readBoxHdrChar(data []byte, boxMaxSize uint32, mgr *event.Manager) (box Box, nbBytesRead uint32, ok bool) {
	if boxMaxSize < 8 {
		mgr.Errorf("Cannot handle box of less than 8 bytes\n")
		return box, 0, false
	}

	box.Length = cio.ReadBytes(data[0:], 4)
	box.Type = cio.ReadBytes(data[4:], 4)
	nbBytesRead = 8

	// XL box
	if box.Length == 1 {
		if boxMaxSize < 16 {
			mgr.Errorf("Cannot handle XL box of less than 16 bytes\n")
			return box, nbBytesRead, false
		}
		xlPartSize := cio.ReadBytes(data[8:], 4)
		nbBytesRead += 4
		if xlPartSize != 0 {
			mgr.Errorf("Cannot handle box sizes higher than 2^32\n")
			return box, nbBytesRead, false
		}
		box.Length = cio.ReadBytes(data[12:], 4)
		nbBytesRead += 4
		if box.Length == 0 {
			mgr.Errorf("Cannot handle box of undefined sizes\n")
			return box, nbBytesRead, false
		}
	} else if box.Length == 0 {
		mgr.Errorf("Cannot handle box of undefined sizes\n")
		return box, nbBytesRead, false
	}
	if box.Length < nbBytesRead {
		mgr.Errorf("Box length is inconsistent.\n")
		return box, nbBytesRead, false
	}
	return box, nbBytesRead, true
}
