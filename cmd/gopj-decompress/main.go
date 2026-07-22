// Command gopj-decompress decodes a JPEG 2000 file (raw codestream or JP2/JPH
// container) and writes the result in a raster format chosen by the output file
// extension. It mirrors the decode-side flags and writer behaviour of the
// reference opj_decompress tool closely enough that PGX, PNM and RAW outputs are
// byte-identical for the supported cases.
//
// Flags:
//
//	-i  input file (.j2k/.j2c/.jpc/.jp2/.jph)  (required)
//	-o  output file; format is chosen by extension:
//	      .pgx            portable graymap-X, one file per component
//	      .pgm/.ppm/.pnm  netpbm (P5/P6/P7)
//	      .raw/.rawl      headerless samples (little-endian on this host)
//	-r  discard the N highest resolutions (reduce)
//	-l  decode only the first N quality layers
//	-d  decode area  x0,y0,x1,y1
//	-c  component subset, comma-separated indices
//	-t  decode only tile index N
//	-strict   reject truncated/non-compliant codestreams
//	-quiet    suppress informational output
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/mgilbir/gopenjpeg"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR -> gopj-decompress: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		in     = flag.String("i", "", "input JPEG 2000 file")
		out    = flag.String("o", "", "output file (format by extension)")
		reduce = flag.Uint("r", 0, "discard the N highest resolutions")
		layers = flag.Uint("l", 0, "decode only the first N quality layers")
		area   = flag.String("d", "", "decode area x0,y0,x1,y1")
		comps  = flag.String("c", "", "component subset, comma-separated")
		tile   = flag.Int("t", -1, "decode only tile index N")
		strict  = flag.Bool("strict", false, "strict conformance mode")
		quiet   = flag.Bool("quiet", false, "suppress informational output")
		threads = flag.String("threads", "1", "worker threads for decode, or ALL_CPUS")
	)
	flag.Parse()

	nThreads := 1
	if *threads == "ALL_CPUS" {
		nThreads = runtime.NumCPU()
	} else if n, perr := strconv.Atoi(*threads); perr == nil && n > 0 {
		nThreads = n
	}

	if *in == "" || *out == "" {
		flag.Usage()
		return fmt.Errorf("both -i and -o are required")
	}

	format, err := outputFormat(*out)
	if err != nil {
		return err
	}

	opts := []gopenjpeg.Option{
		gopenjpeg.WithReduce(uint32(*reduce)),
		gopenjpeg.WithLayers(uint32(*layers)),
		gopenjpeg.WithStrictMode(*strict),
		gopenjpeg.WithConcurrency(nThreads),
	}
	if !*quiet {
		opts = append(opts,
			gopenjpeg.WithWarningHandler(func(s string) { fmt.Fprintf(os.Stderr, "[WARNING] %s", s) }),
			gopenjpeg.WithErrorHandler(func(s string) { fmt.Fprintf(os.Stderr, "[ERROR] %s", s) }),
		)
	}
	if *area != "" {
		a, err := parseArea(*area)
		if err != nil {
			return err
		}
		opts = append(opts, gopenjpeg.WithDecodeArea(a[0], a[1], a[2], a[3]))
	}
	if *comps != "" {
		cs, err := parseComps(*comps)
		if err != nil {
			return err
		}
		opts = append(opts, gopenjpeg.WithComponents(cs...))
	}
	if *tile >= 0 {
		opts = append(opts, gopenjpeg.WithTile(*tile))
	}

	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()

	img, err := gopenjpeg.Decode(f, opts...)
	if err != nil {
		return err
	}

	// Reproduce opj_decompress's post-decode colour handling (sYCC/CMYK/eYCC to
	// sRGB, plus the colour-space label heuristic). ICC-managed images cannot be
	// colour-converted by this port; warn and write the raw components.
	if err := img.ConvertToRGB(); err != nil {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "[WARNING] colour conversion skipped: %v\n", err)
		}
	}

	switch format {
	case fmtPGX:
		return writePGX(img, *out)
	case fmtPNM:
		return writePNM(img, *out)
	case fmtRAW:
		return writeRAW(img, *out)
	default:
		return fmt.Errorf("unsupported output format for %s", *out)
	}
}

type outFmt int

const (
	fmtUnknown outFmt = iota
	fmtPGX
	fmtPNM
	fmtRAW
)

func outputFormat(name string) (outFmt, error) {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".pgx"):
		return fmtPGX, nil
	case strings.HasSuffix(l, ".pgm"), strings.HasSuffix(l, ".ppm"), strings.HasSuffix(l, ".pnm"):
		return fmtPNM, nil
	case strings.HasSuffix(l, ".raw"), strings.HasSuffix(l, ".rawl"):
		return fmtRAW, nil
	default:
		return fmtUnknown, fmt.Errorf("unsupported output extension: %s (want .pgx/.pgm/.ppm/.pnm/.raw/.rawl)", name)
	}
}

func parseArea(s string) ([4]int32, error) {
	var a [4]int32
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return a, fmt.Errorf("decode area must be x0,y0,x1,y1: %q", s)
	}
	for i, p := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 32)
		if err != nil {
			return a, fmt.Errorf("decode area component %q: %w", p, err)
		}
		a[i] = int32(v)
	}
	return a, nil
}

func parseComps(s string) ([]uint32, error) {
	parts := strings.Split(s, ",")
	cs := make([]uint32, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("component index %q: %w", p, err)
		}
		cs = append(cs, uint32(v))
	}
	return cs, nil
}
