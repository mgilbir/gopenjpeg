package main

import (
	"fmt"
	"os"
	"strings"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// readerParams carries the loader-side parameters opj_compress keeps in
// opj_cparameters_t and hands to every convert.c reader: the -d image offset,
// the -s sub-sampling factors and the -mct mode (which the RAW loader uses to
// pick a colour space). The sub-sampling factors are a *loader* parameter in
// OpenJPEG — the library never reads cparameters.subsampling_dx/dy — so they
// have to reach the image here or the -s flag is a no-op (C14).
type readerParams struct {
	offX, offY int
	subX, subY int
	mctMode    int // -1 if unset
	warn       func(string)
}

func (rp readerParams) warnf(format string, args ...any) {
	if rp.warn != nil {
		rp.warn(fmt.Sprintf(format, args...))
	}
}

// maxSamples caps the sample count of a single component, matching the
// "width > INT_MAX / height" guard of convert.c's pnmtoimage. It is a backstop
// for the per-reader "does the file actually hold this much data" checks below,
// and it keeps w*h inside an int on every platform.
const maxSamples = 1<<31 - 1

// readInput dispatches to a format reader by file extension, porting the input
// format sniffing of opj_compress (get_file_format).
func readInput(path string, raw *rawGeometry, rp readerParams) (*gopenjpeg.Image, error) {
	switch strings.ToLower(extOf(path)) {
	case "pnm", "pgm", "ppm", "pbm", "pam":
		return readPNM(path, rp)
	case "pgx":
		return readPGX(path, rp)
	case "raw", "yuv":
		if raw == nil {
			return nil, fmt.Errorf("invalid raw or yuv image parameters: the -F option is required for raw input")
		}
		return readRAW(path, raw, rp, true)
	case "rawl":
		if raw == nil {
			return nil, fmt.Errorf("invalid raw image parameters: the -F option is required for rawl input")
		}
		return readRAW(path, raw, rp, false)
	default:
		return nil, fmt.Errorf("unknown input file format: %s\n"+
			"        known file formats are *.pnm, *.pgm, *.ppm, *.pbm, *.pam, *.pgx, *.raw, *.yuv or *.rawl", path)
	}
}

// intFloorlog2 ports opj_int_floorlog2 for prec derivation.
func intFloorlog2(a int) int {
	l := 0
	for a > 1 {
		a >>= 1
		l++
	}
	return l
}

// hasPrec ports has_prec: the bit count needed to represent val.
func hasPrec(val int) int {
	for i := 1; i <= 16; i++ {
		if val < (1 << i) {
			return i
		}
	}
	return 16
}

// ceildivInt is the integer ceiling division used throughout the codec.
func ceildivInt(a, b int) int { return (a + b - 1) / b }

// checkGeometry rejects degenerate or absurd sample geometry before anything is
// allocated: this is the single guard that keeps a malformed header from
// reaching make() with a bogus length (C7).
func checkGeometry(what string, w, h, numcomps int) error {
	if w < 1 || h < 1 {
		return fmt.Errorf("%s: bad image dimensions %dx%d (both must be >= 1)", what, w, h)
	}
	if numcomps < 1 {
		return fmt.Errorf("%s: bad component count %d (must be >= 1)", what, numcomps)
	}
	if uint64(w)*uint64(h) > maxSamples {
		return fmt.Errorf("%s: image %dx%d too big", what, w, h)
	}
	return nil
}

// checkAvailable rejects a header whose announced sample data cannot possibly
// fit in the remaining input. Bounding the allocation by the actual file size is
// what turns "header says 999999999x999999999" from a makeslice panic into a
// clean error (C7).
func checkAvailable(what string, need uint64, remaining int) error {
	if need > uint64(remaining) {
		return fmt.Errorf("%s: truncated file: header announces %d bytes of sample data, only %d available",
			what, need, remaining)
	}
	return nil
}

// checkSubsampling validates the per-component sub-sampling factors against the
// one byte SIZ gives each of XRsiz/YRsiz.
func checkSubsampling(dx, dy int) error {
	if dx < 1 || dy < 1 || dx > 255 || dy > 255 {
		return fmt.Errorf("bad sub-sampling factor %dx%d (each must be in [1,255])", dx, dy)
	}
	return nil
}

// --- PNM (PBM/PGM/PPM/PNM/PAM) ---

// pnmHeader mirrors struct pnm_header of convert.c.
type pnmHeader struct {
	width, height, maxval, depth, format int
	rgb, rgba, gray, graya, bw           bool
	ok                                   bool
}

// pnmScanner walks the input the way convert.c's read_pnm_header does: line by
// line (fgets with a 250 byte buffer), with skip_white/skip_int/skip_idf
// treating only space and TAB as separators and stopping at CR or LF. Handling
// CR exactly as C does is what makes a CRLF-terminated header read identically
// to an LF one (C16): the terminator belongs to the header line, never to the
// first sample.
type pnmScanner struct {
	data []byte
	pos  int
}

// line returns the next line, including its terminator, capped at 249 bytes
// (fgets(line, 250, ...)).
func (s *pnmScanner) line() ([]byte, bool) {
	if s.pos >= len(s.data) {
		return nil, false
	}
	end := s.pos
	for end < len(s.data) && end-s.pos < 249 {
		c := s.data[end]
		end++
		if c == '\n' {
			break
		}
	}
	l := s.data[s.pos:end]
	s.pos = end
	return l, true
}

// skipWhite ports skip_white: advance over space/TAB, return -1 at CR, LF or
// end of line (C's NUL terminator).
func skipWhite(l []byte, i int) int {
	for i < len(l) {
		switch l[i] {
		case '\n', '\r':
			return -1
		case ' ', '\t', '\v', '\f':
			i++
		default:
			return i
		}
	}
	return -1
}

// skipInt ports skip_int. ok=false mirrors C's NULL return; atEnd mirrors the
// caller's `*s == 0` test (the digits ran to the end of the line buffer).
func skipInt(l []byte, i int) (n, next int, ok bool) {
	j := skipWhite(l, i)
	if j < 0 {
		return 0, -1, false
	}
	start := j
	for j < len(l) && l[j] >= '0' && l[j] <= '9' {
		j++
	}
	// C uses atoi(), which yields 0 for an empty or overflowing run.
	v := 0
	for _, c := range l[start:j] {
		v = v*10 + int(c-'0')
		if v > 1<<31-1 {
			v = 1<<31 - 1
		}
	}
	return v, j, true
}

// skipIdf ports skip_idf (a run of letters or underscores).
func skipIdf(l []byte, i int) (idf string, next int, ok bool) {
	j := skipWhite(l, i)
	if j < 0 {
		return "", -1, false
	}
	start := j
	for j < len(l) {
		c := l[j]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
			j++
			continue
		}
		break
	}
	return string(l[start:j]), j, true
}

// readPNMHeader ports read_pnm_header for P1..P7 (including P7/PAM).
func readPNMHeader(s *pnmScanner) pnmHeader {
	var ph pnmHeader

	l, ok := s.line()
	if !ok {
		return ph
	}
	if len(l) == 0 || l[0] != 'P' {
		return ph
	}
	format := 0
	for i := 1; i < len(l) && l[i] >= '0' && l[i] <= '9'; i++ {
		format = format*10 + int(l[i]-'0')
	}
	if format < 1 || format > 7 {
		return ph
	}
	ph.format = format

	ttype, end := false, false
	for {
		l, ok := s.line()
		if !ok {
			break
		}
		if len(l) > 0 && l[0] == '#' {
			continue
		}
		i := 0
		allowNull := false

		if format == 7 {
			idf, ni, ok := skipIdf(l, i)
			if !ok || ni >= len(l) {
				return ph
			}
			i = ni
			switch idf {
			case "ENDHDR":
				end = true
			case "WIDTH", "HEIGHT", "DEPTH", "MAXVAL":
				v, ni, ok := skipInt(l, i)
				if !ok || ni >= len(l) {
					return ph
				}
				switch idf {
				case "WIDTH":
					ph.width = v
				case "HEIGHT":
					ph.height = v
				case "DEPTH":
					ph.depth = v
				case "MAXVAL":
					ph.maxval = v
				}
				continue
			case "TUPLTYPE":
				t, ni, ok := skipIdf(l, i)
				if !ok || ni >= len(l) {
					return ph
				}
				switch t {
				case "BLACKANDWHITE":
					ph.bw, ttype = true, true
				case "GRAYSCALE":
					ph.gray, ttype = true, true
				case "GRAYSCALE_ALPHA":
					ph.graya, ttype = true, true
				case "RGB":
					ph.rgb, ttype = true, true
				case "RGB_ALPHA":
					ph.rgba, ttype = true, true
				default:
					return ph
				}
				continue
			default:
				return ph
			}
			if end {
				break
			}
			continue
		}

		// Here format is in [1,6].
		if ph.width == 0 {
			v, ni, ok := skipInt(l, i)
			if !ok || ni >= len(l) || v < 1 {
				return ph
			}
			ph.width, i, allowNull = v, ni, true
		}
		if ph.height == 0 {
			v, ni, ok := skipInt(l, i)
			if !ok && allowNull {
				continue
			}
			if !ok || ni >= len(l) || v < 1 {
				return ph
			}
			ph.height, i = v, ni
			if format == 1 || format == 4 {
				break
			}
			allowNull = true
		}
		v, ni, ok := skipInt(l, i)
		if !ok && allowNull {
			continue
		}
		if !ok || ni >= len(l) {
			return ph
		}
		ph.maxval = v
		break
	}

	if format == 2 || format == 3 || format > 4 {
		if ph.maxval < 1 || ph.maxval > 65535 {
			return ph
		}
	}
	if ph.width < 1 || ph.height < 1 {
		return ph
	}
	if format == 7 {
		if !end {
			return ph
		}
		if ph.depth < 1 || ph.depth > 4 {
			return ph
		}
		if ttype {
			ph.ok = true
		}
		return ph
	}
	ph.ok = true
	if format == 1 || format == 4 {
		ph.maxval = 255
	}
	return ph
}

// readPNM ports pnmtoimage for P1..P7.
func readPNM(path string, rp readerParams) (*gopenjpeg.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &pnmScanner{data: data}
	ph := readPNMHeader(s)
	if !ph.ok {
		return nil, fmt.Errorf("pnm: bad or unsupported header (unable to load pnm file)")
	}

	numcomps := 1
	switch ph.format {
	case 1, 2, 4, 5:
		numcomps = 1
	case 3, 6:
		numcomps = 3
	case 7:
		numcomps = ph.depth
	}
	if err := checkGeometry("pnm", ph.width, ph.height, numcomps); err != nil {
		return nil, err
	}
	if err := checkSubsampling(rp.subX, rp.subY); err != nil {
		return nil, err
	}

	w, h := ph.width, ph.height
	prec := hasPrec(ph.maxval)
	if prec < 8 {
		prec = 8
	}

	// Bound the allocation by what the file can actually supply. Each ASCII
	// sample needs at least one digit byte, each binary sample one or two, and
	// P4 packs eight samples per byte.
	samples := uint64(w) * uint64(h) * uint64(numcomps)
	var need uint64
	binary := ph.format == 5 || ph.format == 6 ||
		(ph.format == 7 && (ph.gray || ph.graya || ph.rgb || ph.rgba))
	switch {
	case binary && prec < 9:
		need = samples
	case binary:
		need = samples * 2
	case ph.format == 4:
		need = uint64(ceildivInt(w, 8)) * uint64(h)
	case ph.format == 7 && ph.bw:
		need = samples
	default: // P1/P2/P3 ASCII
		need = samples
	}
	if err := checkAvailable("pnm", need, len(data)-s.pos); err != nil {
		return nil, err
	}

	cs := gopenjpeg.ColorSpaceGray
	if numcomps >= 3 {
		cs = gopenjpeg.ColorSpaceSRGB
	}
	comps := make([]gopenjpeg.Component, numcomps)
	for c := range comps {
		comps[c] = gopenjpeg.Component{
			Dx: uint32(rp.subX), Dy: uint32(rp.subY),
			W: uint32(w), H: uint32(h), Prec: uint32(prec),
			Data: make([]int32, w*h),
		}
	}

	pos := s.pos
	readASCII := func() (int, error) {
		// fscanf("%u"): skip whitespace, optional sign, digits.
		for pos < len(data) {
			c := data[pos]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
				pos++
				continue
			}
			break
		}
		neg := false
		if pos < len(data) && (data[pos] == '+' || data[pos] == '-') {
			neg = data[pos] == '-'
			pos++
		}
		start := pos
		v := 0
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			v = v*10 + int(data[pos]-'0')
			if v > 1<<31-1 {
				v = 1<<31 - 1
			}
			pos++
		}
		if pos == start {
			return 0, fmt.Errorf("pnm: missing data")
		}
		if neg {
			v = -v
		}
		return v, nil
	}

	switch {
	case ph.format == 2 || ph.format == 3:
		for i := 0; i < w*h; i++ {
			for c := 0; c < numcomps; c++ {
				v, err := readASCII()
				if err != nil {
					return nil, err
				}
				comps[c].Data[i] = int32(v * 255 / ph.maxval)
			}
		}
	case binary:
		one := prec < 9
		for i := 0; i < w*h; i++ {
			for c := 0; c < numcomps; c++ {
				if one {
					if pos >= len(data) {
						return nil, fmt.Errorf("pnm: missing data")
					}
					comps[c].Data[i] = int32(data[pos])
					pos++
				} else {
					if pos+1 >= len(data) {
						return nil, fmt.Errorf("pnm: missing data")
					}
					comps[c].Data[i] = int32(uint32(data[pos])<<8 | uint32(data[pos+1]))
					pos += 2
				}
			}
		}
	case ph.format == 1:
		for i := 0; i < w*h; i++ {
			v, err := readASCII()
			if err != nil {
				return nil, err
			}
			if v != 0 {
				comps[0].Data[i] = 0
			} else {
				comps[0].Data[i] = 255
			}
		}
	case ph.format == 4:
		i := 0
		for y := 0; y < h; y++ {
			bit := -1
			uc := byte(0)
			for x := 0; x < w; x++ {
				if bit == -1 {
					bit = 7
					if pos >= len(data) {
						return nil, fmt.Errorf("pnm: missing data")
					}
					uc = data[pos]
					pos++
				}
				if (uc>>uint(bit))&1 != 0 {
					comps[0].Data[i] = 0
				} else {
					comps[0].Data[i] = 255
				}
				bit--
				i++
			}
		}
	case ph.format == 7 && ph.bw:
		for i := 0; i < w*h; i++ {
			if pos >= len(data) {
				return nil, fmt.Errorf("pnm: missing data")
			}
			if data[pos]&1 != 0 {
				comps[0].Data[i] = 0
			} else {
				comps[0].Data[i] = 255
			}
			pos++
		}
	default:
		return nil, fmt.Errorf("pnm: unsupported P%d layout", ph.format)
	}

	img := gopenjpeg.NewImage(cs, uint32(rp.offX), uint32(rp.offY),
		uint32(rp.offX+(w-1)*rp.subX+1), uint32(rp.offY+(h-1)*rp.subY+1), comps)
	return img, nil
}

// --- PGX ---

// pgxScanner reproduces the fscanf format convert.c's pgxtoimage uses:
//
//	"PG%31[ \t]%c%c%31[ \t+-]%d%31[ \t]%d%31[ \t]%d"
//
// The sign is therefore *not* a header token of its own: it is whatever '+'/'-'
// characters appear in the separator run between the endianness tag and the
// precision, attached or not. Both "PG ML +8 W H" and the "PG ML + 8 W H" form
// that opj_decompress (and gopj-decompress) writes are the same header (C13).
type pgxScanner struct {
	data []byte
	pos  int
}

// class consumes up to maxLen bytes from the given set and reports how many it
// consumed (scanf's %[...] conversion, which fails on zero characters).
func (s *pgxScanner) class(set string, maxLen int) int {
	n := 0
	for s.pos < len(s.data) && n < maxLen && strings.IndexByte(set, s.data[s.pos]) >= 0 {
		s.pos++
		n++
	}
	return n
}

// classBytes is class(), returning what it consumed.
func (s *pgxScanner) classBytes(set string, maxLen int) []byte {
	start := s.pos
	s.class(set, maxLen)
	return s.data[start:s.pos]
}

// integer ports scanf's %d: skip leading whitespace, optional sign, digits.
func (s *pgxScanner) integer() (int, bool) {
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			s.pos++
			continue
		}
		break
	}
	neg := false
	if s.pos < len(s.data) && (s.data[s.pos] == '+' || s.data[s.pos] == '-') {
		neg = s.data[s.pos] == '-'
		s.pos++
	}
	start := s.pos
	v := int64(0)
	for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
		v = v*10 + int64(s.data[s.pos]-'0')
		if v > 1<<31-1 {
			v = 1 << 31
		}
		s.pos++
	}
	if s.pos == start {
		return 0, false
	}
	if neg {
		v = -v
	}
	return int(v), true
}

var errBadPGX = fmt.Errorf("bad pgx header, please check input file")

// readPGX ports pgxtoimage (single component, big/little endian, signed or
// unsigned, precision 1..31).
func readPGX(path string, rp readerParams) (*gopenjpeg.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &pgxScanner{data: data}
	if len(data) < 2 || data[0] != 'P' || data[1] != 'G' {
		return nil, errBadPGX
	}
	s.pos = 2
	if s.class(" \t", 31) == 0 {
		return nil, errBadPGX
	}
	if s.pos+1 >= len(data) {
		return nil, errBadPGX
	}
	endian1, endian2 := data[s.pos], data[s.pos+1]
	s.pos += 2
	signtmp := s.classBytes(" \t+-", 31)
	if len(signtmp) == 0 {
		return nil, errBadPGX
	}
	sgnd := strings.IndexByte(string(signtmp), '-') >= 0
	prec, ok := s.integer()
	if !ok {
		return nil, errBadPGX
	}
	if s.class(" \t", 31) == 0 {
		return nil, errBadPGX
	}
	w, ok := s.integer()
	if !ok {
		return nil, errBadPGX
	}
	if s.class(" \t", 31) == 0 {
		return nil, errBadPGX
	}
	h, ok := s.integer()
	if !ok {
		return nil, errBadPGX
	}
	// C consumes exactly one byte (the line terminator) after the header.
	if s.pos < len(data) {
		s.pos++
	}

	bigendian := false
	switch {
	case endian1 == 'M' && endian2 == 'L':
		bigendian = true
	case endian1 == 'L' && endian2 == 'M':
		bigendian = false
	default:
		return nil, errBadPGX
	}
	if w < 1 || h < 1 || prec < 1 || prec > 31 {
		return nil, errBadPGX
	}
	if err := checkGeometry("pgx", w, h, 1); err != nil {
		return nil, err
	}
	if err := checkSubsampling(rp.subX, rp.subY); err != nil {
		return nil, err
	}

	bps := 1
	switch {
	case prec > 16:
		bps = 4
	case prec > 8:
		bps = 2
	}
	if err := checkAvailable("pgx", uint64(w)*uint64(h)*uint64(bps), len(data)-s.pos); err != nil {
		return nil, fmt.Errorf("file too short: %w", err)
	}

	// C's force8 path: a precision below 8 is up-shifted into 8 bits.
	ushift, dshift, adjustS, force8 := 0, 0, 0, false
	if prec < 8 {
		force8 = true
		ushift = 8 - prec
		dshift = prec - ushift
		if sgnd {
			adjustS = 1 << (prec - 1)
		}
		sgnd = false
		prec = 8
	}

	comp := gopenjpeg.Component{
		Dx: uint32(rp.subX), Dy: uint32(rp.subY),
		W: uint32(w), H: uint32(h), Prec: uint32(prec), Sgnd: sgnd,
		Data: make([]int32, w*h),
	}

	pos := s.pos
	max := 0
	for i := 0; i < w*h; i++ {
		var v int
		switch {
		case force8:
			u := int(data[pos])
			pos++
			v = u + adjustS
			if dshift >= 0 {
				v = (v << ushift) + (v >> dshift)
			} else {
				// C evaluates v >> (prec - ushift) with a negative shift here,
				// which is undefined; the up-shift alone is the intended effect.
				v = v << ushift
			}
			if v > max {
				max = v
			}
			comp.Data[i] = int32(uint8(v))
			continue
		case prec == 8:
			u := int(data[pos])
			pos++
			if sgnd {
				v = int(int8(u))
			} else {
				v = u
			}
		case prec <= 16:
			b0, b1 := int(data[pos]), int(data[pos+1])
			pos += 2
			var u int
			if bigendian {
				u = b0<<8 | b1
			} else {
				u = b1<<8 | b0
			}
			if sgnd {
				v = int(int16(u))
			} else {
				v = u
			}
		default:
			b := data[pos : pos+4]
			pos += 4
			var u uint32
			if bigendian {
				u = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
			} else {
				u = uint32(b[3])<<24 | uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0])
			}
			v = int(int32(u))
		}
		if v > max {
			max = v
		}
		comp.Data[i] = int32(v)
	}
	// C recomputes prec from the actual maximum value.
	comp.Prec = uint32(intFloorlog2(max) + 1)

	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, uint32(rp.offX), uint32(rp.offY),
		uint32(rp.offX+(w-1)*rp.subX+1), uint32(rp.offY+(h-1)*rp.subY+1),
		[]gopenjpeg.Component{comp})
	return img, nil
}

// --- RAW / RAWL ---

// rawGeometry holds the -F geometry: width, height, numcomps, bitdepth, sign
// plus per-component sub-sampling.
type rawGeometry struct {
	width, height int
	numcomps      int
	bitdepth      int
	signed        bool
	dx, dy        []int // per-component
}

// subsampledMCTOff ports the opj_compress rule "if a subsampled raw image is
// provided, automatically disable MCT".
func (g *rawGeometry) subsampledMCTOff() bool {
	if g.numcomps > 1 && len(g.dx) > 1 && (g.dx[1] > 1 || g.dy[1] > 1) {
		return true
	}
	if g.numcomps > 2 && len(g.dx) > 2 && (g.dx[2] > 1 || g.dy[2] > 1) {
		return true
	}
	return false
}

// readRAW ports rawtoimage / rawltoimage. bigEndian selects .raw/.yuv (true) vs
// .rawl (false).
func readRAW(path string, g *rawGeometry, rp readerParams, bigEndian bool) (*gopenjpeg.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := checkGeometry("raw", g.width, g.height, g.numcomps); err != nil {
		return nil, err
	}
	if g.bitdepth < 1 {
		return nil, fmt.Errorf("raw: bad bit depth %d (must be >= 1)", g.bitdepth)
	}
	if g.bitdepth > 16 {
		return nil, fmt.Errorf("raw: cannot encode raw components with bit depth higher than 16 bits")
	}
	if err := checkSubsampling(rp.subX, rp.subY); err != nil {
		return nil, err
	}
	numcomps := g.numcomps
	w, h := g.width, g.height

	cs := gopenjpeg.ColorSpaceGray
	switch {
	case numcomps == 1:
		cs = gopenjpeg.ColorSpaceGray
	case numcomps >= 3 && rp.mctMode == 0:
		cs = gopenjpeg.ColorSpaceSYCC
	case numcomps >= 3 && rp.mctMode != 2:
		cs = gopenjpeg.ColorSpaceSRGB
	default:
		cs = gopenjpeg.ColorSpaceUnknown
	}

	gridW := (w-1)*rp.subX + 1
	gridH := (h-1)*rp.subY + 1

	bps := 1
	if g.bitdepth > 8 {
		bps = 2
	}

	// Compute the per-component geometry and the sample count C reads from the
	// file, then check the file can supply it before allocating anything.
	cw := make([]int, numcomps)
	ch := make([]int, numcomps)
	nloop := make([]int, numcomps)
	var need uint64
	for c := 0; c < numcomps; c++ {
		rdx, rdy := 1, 1
		if c < len(g.dx) {
			rdx, rdy = g.dx[c], g.dy[c]
		}
		if err := checkSubsampling(rdx, rdy); err != nil {
			return nil, fmt.Errorf("raw component %d: %w", c, err)
		}
		dx, dy := rp.subX*rdx, rp.subY*rdy
		if err := checkSubsampling(dx, dy); err != nil {
			return nil, fmt.Errorf("raw component %d combined with -s %d,%d: %w", c, rp.subX, rp.subY, err)
		}
		// The encoder addresses component samples with a stride of
		// ceildiv(x1-x0, dx); using floor here is what left the buffer one row
		// short for non-divisible geometry (C51).
		cw[c] = ceildivInt(gridW, dx)
		ch[c] = ceildivInt(gridH, dy)
		nloop[c] = (w * h) / (rdx * rdy) // C's rawtoimage_common loop count
		need += uint64(nloop[c]) * uint64(bps)
	}
	if err := checkAvailable("raw", need, len(data)); err != nil {
		return nil, err
	}

	comps := make([]gopenjpeg.Component, numcomps)
	for c := 0; c < numcomps; c++ {
		rdx, rdy := 1, 1
		if c < len(g.dx) {
			rdx, rdy = g.dx[c], g.dy[c]
		}
		comps[c] = gopenjpeg.Component{
			Dx: uint32(rp.subX * rdx), Dy: uint32(rp.subY * rdy),
			W: uint32(cw[c]), H: uint32(ch[c]),
			Prec: uint32(g.bitdepth), Sgnd: g.signed,
			Data: make([]int32, cw[c]*ch[c]),
		}
	}

	pos := 0
	for c := 0; c < numcomps; c++ {
		limit := len(comps[c].Data)
		for i := 0; i < nloop[c]; i++ {
			var v int32
			if bps == 1 {
				if pos >= len(data) {
					return nil, fmt.Errorf("raw: error reading raw file, end of file probably reached")
				}
				b := data[pos]
				pos++
				if g.signed {
					v = int32(int8(b))
				} else {
					v = int32(b)
				}
			} else {
				if pos+1 >= len(data) {
					return nil, fmt.Errorf("raw: error reading raw file, end of file probably reached")
				}
				b0, b1 := data[pos], data[pos+1]
				pos += 2
				var u uint16
				if bigEndian {
					u = uint16(b0)<<8 | uint16(b1)
				} else {
					u = uint16(b1)<<8 | uint16(b0)
				}
				if g.signed {
					v = int32(int16(u))
				} else {
					v = int32(u)
				}
			}
			if i < limit {
				comps[c].Data[i] = v
			}
		}
	}
	if pos < len(data) {
		rp.warnf("End of raw file not reached... processing anyway\n")
	}

	img := gopenjpeg.NewImage(cs, uint32(rp.offX), uint32(rp.offY),
		uint32(rp.offX+gridW), uint32(rp.offY+gridH), comps)
	return img, nil
}
