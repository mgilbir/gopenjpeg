package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// readInput dispatches to a format reader by file extension, ports the input
// format sniffing of opj_compress (get_file_format). mctMode is the -mct value
// (-1 if unset) used by the RAW reader to choose the colour space.
func readInput(path string, raw *rawGeometry, mctMode, offX, offY int) (*gopenjpeg.Image, error) {
	ext := strings.ToLower(extOf(path))
	switch ext {
	case "pgm", "ppm", "pnm":
		return readPNM(path, offX, offY)
	case "pgx":
		return readPGX(path, offX, offY)
	case "raw":
		if raw == nil {
			return nil, fmt.Errorf("raw input requires -F geometry")
		}
		return readRAW(path, raw, mctMode, true, offX, offY)
	case "rawl":
		if raw == nil {
			return nil, fmt.Errorf("rawl input requires -F geometry")
		}
		return readRAW(path, raw, mctMode, false, offX, offY)
	default:
		return nil, fmt.Errorf("unsupported input format %q", ext)
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

// --- PNM (PGM/PPM/PNM) ---

// readPNM ports pnmtoimage for the P2/P3/P5/P6 subset used by the CLI gate.
func readPNM(path string, offX, offY int) (*gopenjpeg.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 2 || data[0] != 'P' {
		return nil, fmt.Errorf("not a PNM file")
	}
	format := int(data[1] - '0')
	pos := 2
	readInt := func() (int, error) {
		// skip whitespace and comments
		for pos < len(data) {
			c := data[pos]
			if c == '#' {
				for pos < len(data) && data[pos] != '\n' {
					pos++
				}
				continue
			}
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				pos++
				continue
			}
			break
		}
		start := pos
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			pos++
		}
		if pos == start {
			return 0, fmt.Errorf("PNM: expected integer")
		}
		return strconv.Atoi(string(data[start:pos]))
	}

	w, err := readInt()
	if err != nil {
		return nil, err
	}
	h, err := readInt()
	if err != nil {
		return nil, err
	}
	maxval := 255
	if format != 1 && format != 4 {
		maxval, err = readInt()
		if err != nil {
			return nil, err
		}
	}
	// single whitespace after maxval for binary formats
	if pos < len(data) {
		pos++
	}

	var numcomps uint32 = 1
	cs := gopenjpeg.ColorSpaceGray
	if format == 3 || format == 6 {
		numcomps = 3
		cs = gopenjpeg.ColorSpaceSRGB
	}

	prec := hasPrec(maxval)
	if prec < 8 {
		prec = 8
	}

	comps := make([]gopenjpeg.Component, numcomps)
	for c := range comps {
		comps[c] = gopenjpeg.Component{
			Dx: 1, Dy: 1, W: uint32(w), H: uint32(h), Prec: uint32(prec),
			Data: make([]int32, w*h),
		}
	}

	if format == 5 || format == 6 { /* binary */
		one := prec < 9
		for i := 0; i < w*h; i++ {
			for c := uint32(0); c < numcomps; c++ {
				if one {
					if pos >= len(data) {
						return nil, fmt.Errorf("PNM: missing data")
					}
					comps[c].Data[i] = int32(data[pos])
					pos++
				} else {
					if pos+1 >= len(data) {
						return nil, fmt.Errorf("PNM: missing data")
					}
					comps[c].Data[i] = int32(uint32(data[pos])<<8 | uint32(data[pos+1]))
					pos += 2
				}
			}
		}
	} else { /* ascii P2/P3 */
		for i := 0; i < w*h; i++ {
			for c := uint32(0); c < numcomps; c++ {
				v, err := readInt()
				if err != nil {
					return nil, err
				}
				comps[c].Data[i] = int32(v * 255 / maxval)
			}
		}
	}

	img := gopenjpeg.NewImage(cs, uint32(offX), uint32(offY),
		uint32(offX+(w-1)+1), uint32(offY+(h-1)+1), comps)
	return img, nil
}

// --- PGX ---

// readPGX ports pgxtoimage (single component, big/little endian, signed/unsigned).
func readPGX(path string, offX, offY int) (*gopenjpeg.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	// Header: PG <endian> [sign]<prec> <w> <h>
	var header strings.Builder
	// read until we've consumed the header line
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return nil, err
	}
	header.WriteString(line)
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "PG" {
		return nil, fmt.Errorf("bad pgx header")
	}
	endian := fields[1]
	bigendian := endian == "ML"
	if endian != "ML" && endian != "LM" {
		return nil, fmt.Errorf("bad pgx endianness")
	}
	signField := fields[2]
	sgnd := strings.HasPrefix(signField, "-")
	precStr := strings.TrimLeft(signField, "+-")
	prec, err := strconv.Atoi(precStr)
	if err != nil {
		return nil, err
	}
	w, err := strconv.Atoi(fields[3])
	if err != nil {
		return nil, err
	}
	h, err := strconv.Atoi(fields[4])
	if err != nil {
		return nil, err
	}

	comp := gopenjpeg.Component{
		Dx: 1, Dy: 1, W: uint32(w), H: uint32(h), Prec: uint32(prec),
		Sgnd: sgnd, Data: make([]int32, w*h),
	}

	readByte := func() (int, error) {
		b, err := r.ReadByte()
		return int(b), err
	}
	max := 0
	for i := 0; i < w*h; i++ {
		var v int
		if prec <= 8 {
			b, err := readByte()
			if err != nil {
				return nil, err
			}
			if sgnd {
				v = int(int8(b))
			} else {
				v = b
			}
		} else {
			b0, err := readByte()
			if err != nil {
				return nil, err
			}
			b1, err := readByte()
			if err != nil {
				return nil, err
			}
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
		}
		if v > max {
			max = v
		}
		comp.Data[i] = int32(v)
	}
	// C recomputes prec from the actual maximum value.
	comp.Prec = uint32(intFloorlog2(max) + 1)

	cs := gopenjpeg.ColorSpaceGray
	img := gopenjpeg.NewImage(cs, uint32(offX), uint32(offY),
		uint32(offX+(w-1)+1), uint32(offY+(h-1)+1), []gopenjpeg.Component{comp})
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

// readRAW ports rawtoimage / rawltoimage. bigEndian selects .raw (true) vs
// .rawl (false).
func readRAW(path string, g *rawGeometry, mctMode int, bigEndian bool, offX, offY int) (*gopenjpeg.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	numcomps := g.numcomps

	cs := gopenjpeg.ColorSpaceGray
	switch {
	case numcomps == 1:
		cs = gopenjpeg.ColorSpaceGray
	case numcomps >= 3 && mctMode == 0:
		cs = gopenjpeg.ColorSpaceSYCC
	case numcomps >= 3 && mctMode != 2:
		cs = gopenjpeg.ColorSpaceSRGB
	default:
		cs = gopenjpeg.ColorSpaceUnknown
	}

	w, h := g.width, g.height
	comps := make([]gopenjpeg.Component, numcomps)
	for c := 0; c < numcomps; c++ {
		dx, dy := 1, 1
		if c < len(g.dx) {
			dx, dy = g.dx[c], g.dy[c]
		}
		cw := w / dx
		ch := h / dy
		comps[c] = gopenjpeg.Component{
			Dx: uint32(dx), Dy: uint32(dy), W: uint32(cw), H: uint32(ch),
			Prec: uint32(g.bitdepth), Sgnd: g.signed, Data: make([]int32, cw*ch),
		}
	}

	pos := 0
	if g.bitdepth <= 8 {
		for c := 0; c < numcomps; c++ {
			nloop := len(comps[c].Data)
			for i := 0; i < nloop; i++ {
				if pos >= len(data) {
					return nil, fmt.Errorf("raw: end of file")
				}
				b := data[pos]
				pos++
				if g.signed {
					comps[c].Data[i] = int32(int8(b))
				} else {
					comps[c].Data[i] = int32(b)
				}
			}
		}
	} else if g.bitdepth <= 16 {
		for c := 0; c < numcomps; c++ {
			nloop := len(comps[c].Data)
			for i := 0; i < nloop; i++ {
				if pos+1 >= len(data) {
					return nil, fmt.Errorf("raw: end of file")
				}
				b0, b1 := data[pos], data[pos+1]
				pos += 2
				var u int
				if bigEndian {
					u = int(b0)<<8 | int(b1)
				} else {
					u = int(b1)<<8 | int(b0)
				}
				if g.signed {
					comps[c].Data[i] = int32(int16(u))
				} else {
					comps[c].Data[i] = int32(u)
				}
			}
		}
	} else {
		return nil, fmt.Errorf("raw: bitdepth > 16 unsupported")
	}

	img := gopenjpeg.NewImage(cs, uint32(offX), uint32(offY),
		uint32(offX+(w-1)+1), uint32(offY+(h-1)+1), comps)
	return img, nil
}
