// Command gopj-dump prints the main-header structure of a JPEG 2000 file
// (raw codestream or JP2/JPH container). It approximates the information
// reported by the reference opj_dump tool: image and tile geometry, per-
// component precision/sign/sub-sampling, colour space, and the JP2 box fields.
//
// Exact textual parity with opj_dump is NOT a goal; the output is structured and
// complete but formatted independently. The reference opj_dump groups fields
// under "Image info", "Codestream info from main header" and "JP2 info" headers
// and prints coding parameters (progression order, decomposition levels, code-
// block size, quantisation) that this tool omits, since the public ReadInfo API
// surfaces geometry/colour metadata rather than the full coding-style tables.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mgilbir/gopenjpeg"
)

func main() {
	in := flag.String("i", "", "input JPEG 2000 file")
	flag.Parse()
	if *in == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := dump(*in, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR -> gopj-dump: %v\n", err)
		os.Exit(1)
	}
}

func dump(path string, out *os.File) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := gopenjpeg.ReadInfo(f)
	if err != nil {
		return err
	}

	fmtName := "raw codestream (J2K)"
	if info.IsJP2 {
		fmtName = "JP2/JPH container"
	}
	fmt.Fprintf(out, "Format: %s\n", fmtName)
	fmt.Fprintln(out, "Image info {")
	fmt.Fprintf(out, "  x0=%d, y0=%d, x1=%d, y1=%d\n", info.X0, info.Y0, info.X1, info.Y1)
	fmt.Fprintf(out, "  width=%d, height=%d\n", info.X1-info.X0, info.Y1-info.Y0)
	fmt.Fprintf(out, "  numcomps=%d\n", len(info.Components))
	fmt.Fprintf(out, "  color_space=%s\n", info.ColorSpace)
	for i, c := range info.Components {
		fmt.Fprintf(out, "  comp[%d] { dx=%d, dy=%d, prec=%d, sgnd=%t }\n",
			i, c.Dx, c.Dy, c.Prec, c.Sgnd)
	}
	fmt.Fprintln(out, "}")

	fmt.Fprintln(out, "Codestream info from main header {")
	fmt.Fprintf(out, "  tx0=%d, ty0=%d\n", info.TileX0, info.TileY0)
	fmt.Fprintf(out, "  tdx=%d, tdy=%d\n", info.TileWidth, info.TileHeight)
	fmt.Fprintf(out, "  tw=%d, th=%d (numtiles=%d)\n", info.NumTilesX, info.NumTilesY,
		info.NumTilesX*info.NumTilesY)
	fmt.Fprintln(out, "}")

	if info.IsJP2 {
		fmt.Fprintln(out, "JP2 info {")
		fmt.Fprintf(out, "  brand=%s\n", fourCC(info.Brand))
		fmt.Fprintf(out, "  colr method=%d, enumcs=%d\n", info.Meth, info.EnumCS)
		fmt.Fprintf(out, "  icc_profile_len=%d\n", info.ICCLen)
		fmt.Fprintf(out, "  palette=%t (channels=%d)\n", info.HasPalette, info.PaletteChans)
		fmt.Fprintf(out, "  cdef_channels=%d\n", info.CdefChannels)
		fmt.Fprintln(out, "}")
	}
	return nil
}

// fourCC renders a 4-byte box/brand code as its ASCII signature.
func fourCC(v uint32) string {
	b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	for i, c := range b {
		if c < 0x20 || c > 0x7e {
			b[i] = '.'
		}
	}
	return string(b)
}
