package cio

import "io"

// memReader backs an in-memory input stream: a byte slice with a cursor.
type memReader struct {
	data []byte
	pos  int64
}

// memRead ports the behaviour of opj_read_from_file over a byte slice: read up
// to len(buf) bytes from the cursor, returning the count, or -1 (like fread
// returning 0) when the cursor is at or past the end.
func memRead(buf []byte, userData any) int {
	r := userData.(*memReader)
	if r.pos >= int64(len(r.data)) {
		return -1
	}
	n := copy(buf, r.data[r.pos:])
	r.pos += int64(n)
	return n
}

// memSkip ports opj_skip_from_file (fseek SEEK_CUR) over a byte slice: advance
// the cursor by nb and report success. Like fseek on a regular file, seeking
// past the end succeeds.
func memSkip(nb int64, userData any) int64 {
	r := userData.(*memReader)
	r.pos += nb
	return nb
}

// memSeek ports opj_seek_from_file (fseek SEEK_SET) over a byte slice.
func memSeek(nb int64, userData any) bool {
	r := userData.(*memReader)
	r.pos = nb
	return true
}

// NewMemoryInputStream constructs an input Stream over an in-memory byte
// slice. It mirrors opj_stream_create_default_file_stream for a memory-mapped
// file: read/skip/seek functions plus user_data_length set to len(data).
func NewMemoryInputStream(data []byte) *Stream {
	s := newStream(ChunkSize, true)
	r := &memReader{data: data}
	s.userData = r
	s.readFnPtr = memRead
	s.skipFnPtr = memSkip
	s.seekFnPtr = memSeek
	s.seekable = true
	s.SetUserDataLength(uint64(len(data)))
	return s
}

// memWriter backs an in-memory output stream: a growable byte slice with a
// write cursor. Writing past the current length extends the slice; skipping or
// seeking past the length zero-fills the gap, matching how writing past EOF on
// a regular file grows it.
type memWriter struct {
	data []byte
	pos  int64
}

func (w *memWriter) ensure(n int64) {
	if int64(len(w.data)) < n {
		grown := make([]byte, n)
		copy(grown, w.data)
		w.data = grown
	}
}

func memWrite(buf []byte, userData any) int {
	w := userData.(*memWriter)
	w.ensure(w.pos + int64(len(buf)))
	n := copy(w.data[w.pos:], buf)
	w.pos += int64(n)
	return n
}

func memWriteSkip(nb int64, userData any) int64 {
	w := userData.(*memWriter)
	w.pos += nb
	w.ensure(w.pos)
	return nb
}

func memWriteSeek(nb int64, userData any) bool {
	w := userData.(*memWriter)
	w.pos = nb
	w.ensure(w.pos)
	return true
}

// NewMemoryOutputStream constructs an output Stream that accumulates written
// bytes in memory. Retrieve the result with (*Stream).Bytes after flushing.
func NewMemoryOutputStream() *Stream {
	s := newStream(ChunkSize, false)
	w := &memWriter{}
	s.userData = w
	s.writeFnPtr = memWrite
	s.skipFnPtr = memWriteSkip
	s.seekFnPtr = memWriteSeek
	s.seekable = true
	return s
}

// Bytes returns the accumulated bytes of an in-memory output Stream. It must be
// called after Flush. It returns nil for streams not created with
// NewMemoryOutputStream.
func (s *Stream) Bytes() []byte {
	if w, ok := s.userData.(*memWriter); ok {
		return w.data
	}
	return nil
}

// rsReader backs an input stream over an io.ReadSeeker (e.g. *os.File).
type rsReader struct {
	rs io.ReadSeeker
}

// rsRead ports opj_read_from_file: read up to len(buf) bytes, looping to fill
// the buffer like fread, returning the count or -1 at end-of-stream (fread
// returning 0).
func rsRead(buf []byte, userData any) int {
	r := userData.(*rsReader)
	total := 0
	for total < len(buf) {
		n, err := r.rs.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
		if n == 0 {
			break
		}
	}
	if total == 0 {
		return -1
	}
	return total
}

// rsSkip ports opj_skip_from_file: fseek relative to the current position.
func rsSkip(nb int64, userData any) int64 {
	r := userData.(*rsReader)
	if _, err := r.rs.Seek(nb, io.SeekCurrent); err != nil {
		return -1
	}
	return nb
}

// rsSeek ports opj_seek_from_file: fseek to an absolute position.
func rsSeek(nb int64, userData any) bool {
	r := userData.(*rsReader)
	if _, err := r.rs.Seek(nb, io.SeekStart); err != nil {
		return false
	}
	return true
}

// NewReadSeekerInputStream constructs an input Stream over an io.ReadSeeker.
// It determines the user data length the way opj_get_data_length_from_file
// does — seek to end, record the offset, seek back to start — and installs the
// read/skip/seek functions. It returns an error if the initial length probe
// fails.
func NewReadSeekerInputStream(rs io.ReadSeeker) (*Stream, error) {
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	s := newStream(ChunkSize, true)
	s.userData = &rsReader{rs: rs}
	s.readFnPtr = rsRead
	s.skipFnPtr = rsSkip
	s.seekFnPtr = rsSeek
	s.seekable = true
	s.SetUserDataLength(uint64(end))
	return s, nil
}
