package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mgilbir/gopenjpeg"
)

// opjVersion is the OpenJPEG version string embedded in PNM comment headers. It
// must match the oracle build for byte-identical PNM output.
const opjVersion = "2.5.4"

// clamp ports the static clamp() helper in convert.c: clamp a sample to the
// representable range for its precision and signedness.
func clamp(value int32, prec uint32, sgnd bool) int32 {
	if sgnd {
		switch {
		case prec <= 8:
			return clampRange(value, -128, 127)
		case prec <= 16:
			return clampRange(value, -32768, 32767)
		default:
			return value
		}
	}
	switch {
	case prec <= 8:
		return clampRange(value, 0, 255)
	case prec <= 16:
		return clampRange(value, 0, 65535)
	default:
		return value
	}
}

func clampRange(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// writePGX ports imagetopgx: one big-endian .pgx file per component, named
// <base>_<compno>.pgx.
func writePGX(img *gopenjpeg.Image, outfile string) error {
	if !strings.HasSuffix(outfile, ".pgx") {
		return fmt.Errorf("pgx output must end in .pgx: %s", outfile)
	}
	base := outfile[:len(outfile)-4]
	for compno := 0; compno < img.NumComponents(); compno++ {
		c := img.Component(compno)
		name := fmt.Sprintf("%s_%d.pgx", base, compno)
		f, err := os.Create(name)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		sign := byte('+')
		if c.Sgnd {
			sign = '-'
		}
		fmt.Fprintf(w, "PG ML %c %d %d %d\n", sign, c.Prec, c.W, c.H)

		var nbytes int
		switch {
		case c.Prec <= 8:
			nbytes = 1
		case c.Prec <= 16:
			nbytes = 2
		default:
			nbytes = 4
		}
		n := int(c.W) * int(c.H)
		if nbytes == 1 {
			for i := 0; i < n; i++ {
				var v int32
				if c.Prec == 8 && !c.Sgnd {
					v = clampRange(c.Data[i], 0, 255)
				} else {
					v = clamp(c.Data[i], c.Prec, c.Sgnd)
				}
				w.WriteByte(byte(v))
			}
		} else {
			for i := 0; i < n; i++ {
				val := clamp(c.Data[i], c.Prec, c.Sgnd)
				for j := nbytes - 1; j >= 0; j-- {
					w.WriteByte(byte(val >> (uint(j) * 8)))
				}
			}
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// areCompsSimilar ports are_comps_similar.
func areCompsSimilar(img *gopenjpeg.Image) bool {
	c0 := img.Component(0)
	for i := 1; i < img.NumComponents(); i++ {
		ci := img.Component(i)
		if c0.Dx != ci.Dx || c0.Dy != ci.Dy ||
			(i <= 2 && (c0.Prec != ci.Prec || c0.Sgnd != ci.Sgnd)) {
			return false
		}
	}
	return true
}

// writePNM ports imagetopnm (force_split == 0). It writes a combined P6/P7 file
// for similar multi-component images, a single P5 for greyscale, or per-plane
// P5 (.pgm) files otherwise, matching the reference naming and byte layout.
func writePNM(img *gopenjpeg.Image, outfile string) error {
	prec := img.Component(0).Prec
	if prec > 16 {
		return fmt.Errorf("imagetopnm: precision %d is larger than 16", prec)
	}
	ncomp := img.NumComponents()

	// want_gray: second-to-last character of the filename is 'g'/'G' (.pgm).
	wantGray := false
	if len(outfile) >= 2 {
		ch := outfile[len(outfile)-2]
		wantGray = ch == 'g' || ch == 'G'
	}
	if wantGray {
		ncomp = 1
	}

	adjust := func(c gopenjpeg.Component) int32 {
		if c.Sgnd {
			return 1 << (c.Prec - 1)
		}
		return 0
	}

	if ncomp >= 2 && areCompsSimilar(img) {
		f, err := os.Create(outfile)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		two := prec > 8
		triple := ncomp > 2
		c0 := img.Component(0)
		wr, hr := int(c0.W), int(c0.H)
		max := (1 << prec) - 1
		hasAlpha := ncomp == 4 || ncomp == 2

		var c1, c2, ca gopenjpeg.Component
		if triple {
			c1 = img.Component(1)
			c2 = img.Component(2)
		}
		var adjA int32
		if hasAlpha {
			ca = img.Component(ncomp - 1)
			tt := "GRAYSCALE_ALPHA"
			if triple {
				tt = "RGB_ALPHA"
			}
			fmt.Fprintf(w, "P7\n# OpenJPEG-%s\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL %d\nTUPLTYPE %s\nENDHDR\n",
				opjVersion, wr, hr, ncomp, max, tt)
			adjA = adjust(ca)
		} else {
			fmt.Fprintf(w, "P6\n# OpenJPEG-%s\n%d %d\n%d\n", opjVersion, wr, hr, max)
		}
		adjR := adjust(c0)
		var adjG, adjB int32
		if triple {
			adjG = adjust(c1)
			adjB = adjust(c2)
		}

		clampW := func(v int32) int32 { return clampRange(v, 0, 65535) }
		clampB := func(v int32) int32 { return clampRange(v, 0, 255) }
		n := wr * hr
		for i := 0; i < n; i++ {
			if two {
				v := clampW(c0.Data[i] + adjR)
				w.WriteByte(byte(v >> 8))
				w.WriteByte(byte(v))
				if triple {
					v = clampW(c1.Data[i] + adjG)
					w.WriteByte(byte(v >> 8))
					w.WriteByte(byte(v))
					v = clampW(c2.Data[i] + adjB)
					w.WriteByte(byte(v >> 8))
					w.WriteByte(byte(v))
				}
				if hasAlpha {
					v = clampW(ca.Data[i] + adjA)
					w.WriteByte(byte(v >> 8))
					w.WriteByte(byte(v))
				}
				continue
			}
			w.WriteByte(byte(clampB(c0.Data[i])))
			if triple {
				w.WriteByte(byte(clampB(c1.Data[i])))
				w.WriteByte(byte(clampB(c2.Data[i])))
			}
			if hasAlpha {
				w.WriteByte(byte(clampB(ca.Data[i])))
			}
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}

	// YUV or MONO: one P5 file per component.
	for compno := 0; compno < ncomp; compno++ {
		c := img.Component(compno)
		var destname string
		if ncomp > 1 {
			base := outfile[:len(outfile)-4]
			destname = fmt.Sprintf("%s_%d.pgm", base, compno)
		} else {
			destname = outfile
		}
		f, err := os.Create(destname)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		wr, hr := int(c.W), int(c.H)
		cprec := c.Prec
		max := (1 << cprec) - 1
		fmt.Fprintf(w, "P5\n#OpenJPEG-%s\n%d %d\n%d\n", opjVersion, wr, hr, max)
		adjR := adjust(c)
		n := wr * hr
		if cprec > 8 {
			for i := 0; i < n; i++ {
				v := clampRange(c.Data[i]+adjR, 0, 65535)
				w.WriteByte(byte(v >> 8))
				w.WriteByte(byte(v))
			}
		} else {
			for i := 0; i < n; i++ {
				v := clampRange(c.Data[i]+adjR, 0, 255)
				w.WriteByte(byte(v))
			}
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// writeRAW ports imagetoraw_common. NOTE: the reference ignores its big_endian
// argument (it is "(void)big_endian") and writes 16-bit samples in host byte
// order via a union. On the little-endian build host this makes .raw and .rawl
// byte-identical, both little-endian; this port reproduces that exactly.
func writeRAW(img *gopenjpeg.Image, outfile string) error {
	numcomps := img.NumComponents()
	x0, y0, x1, y1 := img.Bounds()
	_ = x0
	_ = y0
	if numcomps == 0 || x1 == 0 || y1 == 0 {
		return fmt.Errorf("invalid raw image parameters")
	}
	check := numcomps
	if check > 4 {
		check = 4
	}
	c0 := img.Component(0)
	for compno := 1; compno < check; compno++ {
		ci := img.Component(compno)
		if c0.Dx != ci.Dx || c0.Dy != ci.Dy || c0.Prec != ci.Prec || c0.Sgnd != ci.Sgnd {
			return fmt.Errorf("imagetoraw_common: all components shall have the same subsampling, bit depth and sign")
		}
	}
	f, err := os.Create(outfile)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for compno := 0; compno < numcomps; compno++ {
		c := img.Component(compno)
		n := int(c.W) * int(c.H)
		mask := (int32(1) << c.Prec) - 1
		switch {
		case c.Prec <= 8:
			for i := 0; i < n; i++ {
				curr := c.Data[i]
				if c.Sgnd {
					curr = clampRange(curr, -128, 127)
				} else {
					curr = clampRange(curr, 0, 255)
				}
				w.WriteByte(byte(curr & mask))
			}
		case c.Prec <= 16:
			for i := 0; i < n; i++ {
				curr := c.Data[i]
				if c.Sgnd {
					curr = clampRange(curr, -32768, 32767)
				} else {
					curr = clampRange(curr, 0, 65535)
				}
				v := uint16(curr & mask)
				// host (little-endian) byte order, as the reference does.
				w.WriteByte(byte(v))
				w.WriteByte(byte(v >> 8))
			}
		default:
			w.Flush()
			f.Close()
			return fmt.Errorf("more than 16 bits per component not handled")
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
