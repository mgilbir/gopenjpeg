package cio

import (
	"errors"
	"io"

	"github.com/mgilbir/gopenjpeg/internal/event"
)

// ChunkSize ports OPJ_J2K_STREAM_CHUNK_SIZE (openjpeg.h): the default internal
// buffer size (1 MiB) used by the default stream constructors.
const ChunkSize = 0x100000

// Stream status flags, porting the OPJ_STREAM_STATUS_* defines in cio.h.
const (
	statusOutput uint32 = 0x1
	statusInput  uint32 = 0x2
	statusEnd    uint32 = 0x4
	statusError  uint32 = 0x8
)

// ErrEnd is returned by Read/Skip when the underlying C code would return
// (OPJ_SIZE_T)-1 / (OPJ_OFF_T)-1, i.e. the stream is at its end and no bytes
// could be transferred. It wraps io.EOF so callers may test either.
var ErrEnd = io.EOF

// ErrStream is returned where the C code sets OPJ_STREAM_STATUS_ERROR and
// returns OPJ_FALSE (write/seek failures).
var ErrStream = errors.New("cio: stream error")

// readFn ports opj_stream_read_fn. It fills buf and returns the number of
// bytes read, or -1 to signal end-of-stream (mirroring (OPJ_SIZE_T)-1).
type readFn func(buf []byte, userData any) int

// writeFn ports opj_stream_write_fn. It returns the number of bytes written,
// or -1 on error.
type writeFn func(buf []byte, userData any) int

// skipFn ports opj_stream_skip_fn. It returns the number of bytes skipped, or
// -1 on error.
type skipFn func(nb int64, userData any) int64

// seekFn ports opj_stream_seek_fn. It returns true on success.
type seekFn func(nb int64, userData any) bool

// freeFn ports opj_stream_free_user_data_fn.
type freeFn func(userData any)

// Stream ports opj_stream_private_t: a buffered byte stream over user-provided
// read/write/skip/seek functions. It reproduces the C buffering, partial-read,
// end-of-stream and seek/skip semantics faithfully.
type Stream struct {
	userData       any
	freeUserDataFn freeFn
	userDataLength uint64

	readFnPtr  readFn
	writeFnPtr writeFn
	skipFnPtr  skipFn
	seekFnPtr  seekFn

	storedData []byte // fixed-size internal buffer (m_stored_data)
	currentPos int    // index into storedData (m_current_data - m_stored_data)

	// opjSkip / opjSeek select the read- vs write-oriented skip/seek
	// behaviour, porting the m_opj_skip / m_opj_seek function pointers.
	opjSkip func(s *Stream, size int64, mgr *event.Manager) int64
	opjSeek func(s *Stream, size int64, mgr *event.Manager) bool

	bytesInBuffer int   // m_bytes_in_buffer
	byteOffset    int64 // m_byte_offset
	bufferSize    int   // m_buffer_size
	status        uint32

	// seekable replaces the C pointer-identity test in opj_stream_has_seek
	// (m_seek_fn != opj_stream_default_seek): constructors that install a
	// working seek function set it to true.
	seekable bool
}

// newStream ports opj_stream_create: allocate a stream with an internal buffer
// of bufferSize bytes, configured for input or output. Default user functions
// (which always error) are installed; constructors override them.
func newStream(bufferSize int, isInput bool) *Stream {
	s := &Stream{
		bufferSize: bufferSize,
		storedData: make([]byte, bufferSize),
	}
	if isInput {
		s.status |= statusInput
		s.opjSkip = (*Stream).readSkip
		s.opjSeek = (*Stream).readSeek
	} else {
		s.status |= statusOutput
		s.opjSkip = (*Stream).writeSkip
		s.opjSeek = (*Stream).writeSeek
	}
	s.readFnPtr = defaultRead
	s.writeFnPtr = defaultWrite
	s.skipFnPtr = defaultSkip
	s.seekFnPtr = defaultSeek
	return s
}

// defaultRead ports opj_stream_default_read: always signals end-of-stream.
func defaultRead(buf []byte, userData any) int { return -1 }

// defaultWrite ports opj_stream_default_write: always errors.
func defaultWrite(buf []byte, userData any) int { return -1 }

// defaultSkip ports opj_stream_default_skip: always errors.
func defaultSkip(nb int64, userData any) int64 { return -1 }

// defaultSeek ports opj_stream_default_seek: always fails.
func defaultSeek(nb int64, userData any) bool { return false }

// SetUserDataLength ports opj_stream_set_user_data_length.
func (s *Stream) SetUserDataLength(length uint64) {
	s.userDataLength = length
}

// Destroy ports opj_stream_destroy: invokes the free-user-data callback if set.
func (s *Stream) Destroy() {
	if s == nil {
		return
	}
	if s.freeUserDataFn != nil {
		s.freeUserDataFn(s.userData)
	}
	s.storedData = nil
}

// readData ports opj_stream_read_data. It returns the number of bytes read
// into dst, or -1 if the stream is at its end and nothing could be read.
func (s *Stream) readData(dst []byte, mgr *event.Manager) int {
	pSize := len(dst)
	dstOff := 0
	readNb := 0

	if s.bytesInBuffer >= pSize {
		copy(dst[dstOff:dstOff+pSize], s.storedData[s.currentPos:s.currentPos+pSize])
		s.currentPos += pSize
		s.bytesInBuffer -= pSize
		readNb += pSize
		s.byteOffset += int64(pSize)
		return readNb
	}

	// Remaining data is not sufficient.
	if s.status&statusEnd != 0 {
		readNb += s.bytesInBuffer
		copy(dst[dstOff:dstOff+s.bytesInBuffer], s.storedData[s.currentPos:s.currentPos+s.bytesInBuffer])
		s.currentPos += s.bytesInBuffer
		s.byteOffset += int64(s.bytesInBuffer)
		s.bytesInBuffer = 0
		if readNb != 0 {
			return readNb
		}
		return -1
	}

	// The END flag is not set: copy what we have, then read from the media.
	if s.bytesInBuffer != 0 {
		readNb += s.bytesInBuffer
		copy(dst[dstOff:dstOff+s.bytesInBuffer], s.storedData[s.currentPos:s.currentPos+s.bytesInBuffer])
		s.currentPos = 0
		dstOff += s.bytesInBuffer
		pSize -= s.bytesInBuffer
		s.byteOffset += int64(s.bytesInBuffer)
		s.bytesInBuffer = 0
	} else {
		s.currentPos = 0
	}

	for {
		if pSize < s.bufferSize {
			// Read a whole chunk into the internal buffer.
			n := s.readFnPtr(s.storedData[:s.bufferSize], s.userData)
			if n == -1 {
				mgr.Infof("Stream reached its end !\n")
				s.bytesInBuffer = 0
				s.status |= statusEnd
				if readNb != 0 {
					return readNb
				}
				return -1
			}
			s.bytesInBuffer = n
			if s.bytesInBuffer < pSize {
				readNb += s.bytesInBuffer
				copy(dst[dstOff:dstOff+s.bytesInBuffer], s.storedData[s.currentPos:s.currentPos+s.bytesInBuffer])
				s.currentPos = 0
				dstOff += s.bytesInBuffer
				pSize -= s.bytesInBuffer
				s.byteOffset += int64(s.bytesInBuffer)
				s.bytesInBuffer = 0
			} else {
				readNb += pSize
				copy(dst[dstOff:dstOff+pSize], s.storedData[s.currentPos:s.currentPos+pSize])
				s.currentPos += pSize
				s.bytesInBuffer -= pSize
				s.byteOffset += int64(pSize)
				return readNb
			}
		} else {
			// Direct read into the destination buffer.
			n := s.readFnPtr(dst[dstOff:dstOff+pSize], s.userData)
			if n == -1 {
				mgr.Infof("Stream reached its end !\n")
				s.bytesInBuffer = 0
				s.status |= statusEnd
				if readNb != 0 {
					return readNb
				}
				return -1
			}
			s.bytesInBuffer = n
			if s.bytesInBuffer < pSize {
				readNb += s.bytesInBuffer
				s.currentPos = 0
				dstOff += s.bytesInBuffer
				pSize -= s.bytesInBuffer
				s.byteOffset += int64(s.bytesInBuffer)
				s.bytesInBuffer = 0
			} else {
				readNb += s.bytesInBuffer
				s.byteOffset += int64(s.bytesInBuffer)
				s.currentPos = 0
				s.bytesInBuffer = 0
				return readNb
			}
		}
	}
}

// Read reads up to len(dst) bytes into dst. It returns the number of bytes
// read (which may be less than len(dst) near end-of-stream), or (0, ErrEnd)
// when the C code would return (OPJ_SIZE_T)-1.
func (s *Stream) Read(dst []byte, mgr *event.Manager) (int, error) {
	n := s.readData(dst, mgr)
	if n == -1 {
		return 0, ErrEnd
	}
	return n, nil
}

// writeData ports opj_stream_write_data. It returns the number of bytes
// buffered/written, or -1 on error.
func (s *Stream) writeData(src []byte, mgr *event.Manager) int {
	pSize := len(src)
	srcOff := 0
	writeNb := 0

	if s.status&statusError != 0 {
		return -1
	}

	for {
		remaining := s.bufferSize - s.bytesInBuffer

		if remaining >= pSize {
			copy(s.storedData[s.currentPos:s.currentPos+pSize], src[srcOff:srcOff+pSize])
			s.currentPos += pSize
			s.bytesInBuffer += pSize
			writeNb += pSize
			s.byteOffset += int64(pSize)
			return writeNb
		}

		if remaining != 0 {
			writeNb += remaining
			copy(s.storedData[s.currentPos:s.currentPos+remaining], src[srcOff:srcOff+remaining])
			s.currentPos = 0
			srcOff += remaining
			pSize -= remaining
			s.bytesInBuffer += remaining
			s.byteOffset += int64(remaining)
		}

		if !s.flush(mgr) {
			return -1
		}
	}
}

// Write writes src to the stream. It returns the number of bytes written, or
// an error if the C code would return -1.
func (s *Stream) Write(src []byte, mgr *event.Manager) (int, error) {
	n := s.writeData(src, mgr)
	if n == -1 {
		return 0, ErrStream
	}
	return n, nil
}

// flush ports opj_stream_flush: write the buffered bytes to the media.
func (s *Stream) flush(mgr *event.Manager) bool {
	s.currentPos = 0

	for s.bytesInBuffer != 0 {
		n := s.writeFnPtr(s.storedData[s.currentPos:s.currentPos+s.bytesInBuffer], s.userData)
		if n == -1 {
			s.status |= statusError
			mgr.Infof("Error on writing stream!\n")
			return false
		}
		s.currentPos += n
		s.bytesInBuffer -= n
	}

	s.currentPos = 0
	return true
}

// Flush ports opj_stream_flush with a Go error return.
func (s *Stream) Flush(mgr *event.Manager) error {
	if !s.flush(mgr) {
		return ErrStream
	}
	return nil
}

// readSkip ports opj_stream_read_skip. size must be >= 0.
func (s *Stream) readSkip(size int64, mgr *event.Manager) int64 {
	var skipNb int64

	if int64(s.bytesInBuffer) >= size {
		s.currentPos += int(size)
		s.bytesInBuffer -= int(size)
		skipNb += size
		s.byteOffset += skipNb
		return skipNb
	}

	if s.status&statusEnd != 0 {
		skipNb += int64(s.bytesInBuffer)
		s.currentPos += s.bytesInBuffer
		s.bytesInBuffer = 0
		s.byteOffset += skipNb
		if skipNb != 0 {
			return skipNb
		}
		return -1
	}

	if s.bytesInBuffer != 0 {
		skipNb += int64(s.bytesInBuffer)
		s.currentPos = 0
		size -= int64(s.bytesInBuffer)
		s.bytesInBuffer = 0
	}

	for size > 0 {
		// Do not advance beyond user_data_length, matching the C guard that
		// keeps opj_stream_get_number_byte_left consistent.
		if uint64(s.byteOffset+skipNb+size) > s.userDataLength {
			mgr.Infof("Stream reached its end !\n")

			s.byteOffset += skipNb
			skipNb = int64(s.userDataLength - uint64(s.byteOffset))

			s.readSeek(int64(s.userDataLength), mgr)
			s.status |= statusEnd

			if skipNb != 0 {
				return skipNb
			}
			return -1
		}

		cur := s.skipFnPtr(size, s.userData)
		if cur == -1 {
			mgr.Infof("Stream reached its end !\n")
			s.status |= statusEnd
			s.byteOffset += skipNb
			if skipNb != 0 {
				return skipNb
			}
			return -1
		}
		size -= cur
		skipNb += cur
	}

	s.byteOffset += skipNb
	return skipNb
}

// writeSkip ports opj_stream_write_skip. size must be >= 0.
func (s *Stream) writeSkip(size int64, mgr *event.Manager) int64 {
	var skipNb int64

	if s.status&statusError != 0 {
		return -1
	}

	if !s.flush(mgr) {
		s.status |= statusError
		s.bytesInBuffer = 0
		return -1
	}

	for size > 0 {
		cur := s.skipFnPtr(size, s.userData)
		if cur == -1 {
			mgr.Infof("Stream error!\n")
			s.status |= statusError
			s.byteOffset += skipNb
			if skipNb != 0 {
				return skipNb
			}
			return -1
		}
		size -= cur
		skipNb += cur
	}

	s.byteOffset += skipNb
	return skipNb
}

// Tell ports opj_stream_tell.
func (s *Stream) Tell() int64 {
	return s.byteOffset
}

// NumberByteLeft ports opj_stream_get_number_byte_left.
func (s *Stream) NumberByteLeft() int64 {
	if s.userDataLength != 0 {
		return int64(s.userDataLength) - s.byteOffset
	}
	return 0
}

// Skip ports opj_stream_skip: dispatch to the read- or write-oriented skip.
// size must be >= 0. It returns the number of bytes skipped, or (0, ErrEnd)
// when the C code returns -1.
func (s *Stream) Skip(size int64, mgr *event.Manager) (int64, error) {
	if size < 0 {
		return 0, ErrStream
	}
	n := s.opjSkip(s, size, mgr)
	if n == -1 {
		return 0, ErrEnd
	}
	return n, nil
}

// readSeek ports opj_stream_read_seek.
func (s *Stream) readSeek(size int64, mgr *event.Manager) bool {
	s.currentPos = 0
	s.bytesInBuffer = 0

	if !s.seekFnPtr(size, s.userData) {
		s.status |= statusEnd
		return false
	}
	s.status &^= statusEnd
	s.byteOffset = size
	return true
}

// writeSeek ports opj_stream_write_seek.
func (s *Stream) writeSeek(size int64, mgr *event.Manager) bool {
	if !s.flush(mgr) {
		s.status |= statusError
		return false
	}

	s.currentPos = 0
	s.bytesInBuffer = 0

	if !s.seekFnPtr(size, s.userData) {
		s.status |= statusError
		return false
	}
	s.byteOffset = size
	return true
}

// SeekTo ports opj_stream_seek: dispatch to the read- or write-oriented seek.
// size must be >= 0. (Named SeekTo rather than Seek to avoid clashing with the
// io.Seeker method convention.)
func (s *Stream) SeekTo(size int64, mgr *event.Manager) error {
	if size < 0 {
		return ErrStream
	}
	if !s.opjSeek(s, size, mgr) {
		return ErrStream
	}
	return nil
}

// HasSeek ports opj_stream_has_seek: reports whether a real seek function is
// installed (i.e. not the always-failing default).
func (s *Stream) HasSeek() bool {
	return s.seekable
}
