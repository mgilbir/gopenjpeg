// Command gopj-compress encodes a raster image into a JPEG 2000 codestream
// (.j2k/.j2c) or JP2 container (.jp2), mirroring the flags and defaults of the
// reference opj_compress tool closely enough that the produced codestream is
// byte-identical for the supported cases.
//
// Supported input formats (chosen by extension): PBM/PGM/PPM/PNM/PAM (netpbm
// P1 through P7), PGX, and RAW/YUV/RAWL (headerless, geometry supplied with
// -F). Output format is chosen by the -o extension (.j2k/.j2c/.jpc or
// .jp2/.jph); an unrecognised extension is an error. Run with -h for the flag
// reference.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// usageText mirrors the layout of opj_compress's encode_help_display, restricted
// to the flags this tool implements.
const usageText = `
This is the gopj-compress utility from the gopenjpeg project.
It compresses raster images with the JPEG 2000 algorithm.

Default encoding options:
-------------------------

 * Lossless
 * 1 tile
 * RGB->YCC conversion if at least 3 components
 * Size of precinct : 2^15 x 2^15 (means 1 precinct)
 * Size of code-block : 64 x 64
 * Number of resolutions: 6
 * No SOP marker in the codestream
 * No EPH marker in the codestream
 * No sub-sampling in x or y direction
 * No mode switch activated
 * Progression order: LRCP
 * No ROI upshifted
 * No offset of the origin of the image
 * No offset of the origin of the tiles
 * Reversible DWT 5-3

Parameters:
-----------

Required Parameters:

-i <file>
    Input file.
    Known extensions are <PBM|PGM|PPM|PNM|PAM|PGX|RAW|YUV|RAWL>
-o <compressed file>
    Output file (accepted extensions are j2k, j2c, jpc, jp2 or jph).
-F <width>,<height>,<ncomp>,<bitdepth>,{s,u}@<dx1>x<dy1>:...:<dxn>x<dyn>
    Characteristics of the raw or yuv input image.
    If subsampling is omitted, 1x1 is assumed for all components.
      Example: -F 512,512,3,8,u@1x1:2x2:2x2
    Required only if RAW, YUV or RAWL input file is provided.

Optional Parameters:

-h
    Display this help information.
-r <compression ratio>,<compression ratio>,...
    Different compression ratios for successive layers (use 1 for lossless).
    Options -r and -q cannot be used together.
-q <psnr value>,<psnr value>,...
    Different psnr for successive layers (-q 30,40,50).
    Options -r and -q cannot be used together.
-n <number of resolutions>
    Number of resolutions (DWT decompositions + 1). Default: 6.
-b <cblk width>,<cblk height>
    Code-block size. Default: 64,64.
-c [<prec width>,<prec height>],[<prec width>,<prec height>],...
    Precinct size, highest resolution first. Values must be powers of 2.
-t <tile width>,<tile height>
    Tile size. Default: the whole image, thus one tile.
-T <tile offset X,tile offset Y>
    Offset of the origin of the tiles.
-d <image offset X,image offset Y>
    Offset of the origin of the image.
-p <LRCP|RLCP|RPCL|PCRL|CPRL>
    Progression order. Default: LRCP.
-s <subX,subY>
    Sub-sampling factor. Subsampling bigger than 2 can produce error.
    Default: no subsampling.
-POC <progression order change>/<progression order change>/...
    Progression order change. Every '/'-separated record is applied.
    The syntax of one record is:
    T<tile>=<resStart>,<compStart>,<layerEnd>,<resEnd>,<compEnd>,<progOrder>
      Example: -POC T1=0,0,1,5,3,CPRL/T1=5,0,1,6,3,CPRL
-SOP
    Write SOP marker before each packet.
-EPH
    Write EPH marker after each header packet.
-PLT
    Write PLT marker in tile-part header.
-TLM
    Write TLM marker in main header.
-M <key value>
    Mode switch. [1=BYPASS(LAZY) 2=RESET 4=RESTART(TERMALL)
    8=VSC 16=ERTERM(SEGTERM) 32=SEGMARK(SEGSYM)]. Add the values.
-TP <R|L|C>
    Divide packets of every tile into tile-parts, grouping by
    Resolutions (R), Layers (L) or Components (C).
-ROI c=<component index>,U=<upshifting value>
    Quantization indices upshifted for a component.
-I
    Use the irreversible DWT 9-7.
-G <guard bits>
    Number of quantization guard bits in [0,7].
-C <comment>
    Content of the COM marker.
-mct <0|1|2>
    0: no MCT ; 1: RGB->YCC conversion ; 2: custom MCT (requires -m).
-m <file>
    Use array-based MCT; automatically sets -mct 2.
-cinema2K <24|48>
    Digital Cinema 2K profile compliant codestream.
-cinema4K
    Digital Cinema 4K profile compliant codestream.
-IMF <PROFILE>[,mainlevel=X][,sublevel=Y][,framerate=FPS]
    Interoperable Master Format profile compliant codestream.
    PROFILE is one of 2K, 4K, 8K, 2K_R, 4K_R, 8K_R.
-threads <num_threads|ALL_CPUS>
    Number of worker goroutines used for the encode.
-quiet
    Suppress informational output.
`

// usage prints the help text.
func usage(w *os.File) {
	fmt.Fprint(w, usageText)
}

// parseThreads parses the -threads value: a positive integer, or "ALL_CPUS"
// for runtime.NumCPU() (mirroring opj_compress -threads).
func parseThreads(v string) (int, error) {
	if v == "ALL_CPUS" {
		return runtime.NumCPU(), nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("threads: value must be a positive integer or ALL_CPUS")
	}
	return n, nil
}

// errHelp is the sentinel run returns when -h was given: the caller prints the
// usage text and exits 0.
var errHelp = fmt.Errorf("help requested")

func main() {
	err := run(os.Args[1:])
	switch {
	case err == nil:
		return
	case err == errHelp:
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "ERROR -> gopj-compress: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Help: gopj-compress -h\n")
		os.Exit(1)
	}
}

// cliParams holds the parsed command line.
type cliParams struct {
	input            string
	output           string
	opts             []gopenjpeg.EncodeOption
	rawGeom          *rawGeometry // -F
	mctMode          int          // -1 = unset
	offsetX, offsetY int          // -d image offset
	subX, subY       int          // -s sub-sampling (1,1 = none)
	haveRates        bool         // -r seen
	haveQuality      bool         // -q seen
	haveMCTMatrix    bool         // -m seen
	quiet            bool
}

func run(args []string) error {
	p := cliParams{mctMode: -1, subX: 1, subY: 1}

	i := 0
	next := func() (string, error) {
		i++
		if i >= len(args) {
			return "", fmt.Errorf("missing argument for %s", args[i-1])
		}
		return args[i], nil
	}

	for ; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return fmt.Errorf("unexpected argument %q", a)
		}
		flag := strings.TrimLeft(a, "-")
		switch flag {
		case "i":
			v, err := next()
			if err != nil {
				return err
			}
			p.input = v
		case "o":
			v, err := next()
			if err != nil {
				return err
			}
			p.output = v
		case "r":
			v, err := next()
			if err != nil {
				return err
			}
			rates, err := parseFloatList(v)
			if err != nil {
				return err
			}
			p.haveRates = true
			p.opts = append(p.opts, gopenjpeg.WithRates(rates...))
		case "q":
			v, err := next()
			if err != nil {
				return err
			}
			q, err := parseFloatList(v)
			if err != nil {
				return err
			}
			p.haveQuality = true
			p.opts = append(p.opts, gopenjpeg.WithQualityLayers(q...))
		case "n":
			v, err := next()
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithResolutions(n))
		case "b":
			v, err := next()
			if err != nil {
				return err
			}
			w, h, err := parsePair(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithCodeBlockSize(w, h))
		case "c":
			v, err := next()
			if err != nil {
				return err
			}
			sizes, err := parsePrecincts(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithPrecincts(sizes...))
		case "t":
			v, err := next()
			if err != nil {
				return err
			}
			w, h, err := parsePair(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithTileSize(w, h))
		case "T":
			v, err := next()
			if err != nil {
				return err
			}
			x, y, err := parsePair(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithTileOrigin(x, y))
		case "p":
			v, err := next()
			if err != nil {
				return err
			}
			order, err := parseProgression(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithProgressionOrder(order))
		case "s":
			v, err := next()
			if err != nil {
				return err
			}
			dx, dy, err := parsePair(v)
			if err != nil {
				return fmt.Errorf("'-s' sub-sampling argument error !  [-s dx,dy]")
			}
			if err := checkSubsampling(dx, dy); err != nil {
				return fmt.Errorf("'-s' sub-sampling argument error: %w", err)
			}
			// The library never reads cparameters.subsampling_dx/dy (neither
			// does OpenJPEG's): the factors belong to the image the loaders
			// build, so they are threaded into readInput instead of into an
			// encode option (C14).
			p.subX, p.subY = dx, dy
		case "SOP":
			p.opts = append(p.opts, gopenjpeg.WithSOP())
		case "EPH":
			p.opts = append(p.opts, gopenjpeg.WithEPH())
		case "M":
			v, err := next()
			if err != nil {
				return err
			}
			m, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithModeSwitches(m))
		case "I":
			p.opts = append(p.opts, gopenjpeg.WithIrreversible())
		case "ROI":
			v, err := next()
			if err != nil {
				return err
			}
			compno, shift, err := parseROI(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithROI(compno, shift))
		case "TP":
			v, err := next()
			if err != nil {
				return err
			}
			// opj_compress documents -TP <R|L|C>; anything else is not a
			// tile-part division the encoder understands, and used to be a
			// silent no-op here (C49).
			if v != "R" && v != "L" && v != "C" {
				return fmt.Errorf("bad -TP value %q: expected one of R (resolution), L (layer) or C (component)", v)
			}
			p.opts = append(p.opts, gopenjpeg.WithTileParts(v[0]))
		case "POC":
			v, err := next()
			if err != nil {
				return err
			}
			pocs, err := parsePOC(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithPOC(pocs...))
		case "mct":
			v, err := next()
			if err != nil {
				return err
			}
			m, err := strconv.Atoi(v)
			if err != nil || m < 0 || m > 2 {
				return fmt.Errorf("MCT incorrect value")
			}
			p.mctMode = m
			p.opts = append(p.opts, gopenjpeg.WithMCT(m))
		case "m":
			v, err := next()
			if err != nil {
				return err
			}
			matrix, dc, err := parseMCTFile(v)
			if err != nil {
				return err
			}
			p.haveMCTMatrix = true
			p.opts = append(p.opts, gopenjpeg.WithCustomMCT(matrix, dc))
		case "C":
			v, err := next()
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithComment(v))
		case "PLT":
			p.opts = append(p.opts, gopenjpeg.WithPLT())
		case "TLM":
			p.opts = append(p.opts, gopenjpeg.WithTLM())
		case "G":
			v, err := next()
			if err != nil {
				return err
			}
			g, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithGuardBits(g))
		case "d":
			v, err := next()
			if err != nil {
				return err
			}
			x, y, err := parsePair(v)
			if err != nil {
				return err
			}
			p.offsetX, p.offsetY = x, y
		case "cinema2K":
			v, err := next()
			if err != nil {
				return err
			}
			fps, err := strconv.Atoi(v)
			if err != nil || (fps != 24 && fps != 48) {
				return fmt.Errorf("cinema2K: value must be 24 or 48")
			}
			p.opts = append(p.opts, gopenjpeg.WithCinema2K(fps))
		case "cinema4K":
			p.opts = append(p.opts, gopenjpeg.WithCinema4K())
		case "IMF":
			v, err := next()
			if err != nil {
				return err
			}
			rsiz, err := parseIMF(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithProfile(rsiz))
		case "F":
			v, err := next()
			if err != nil {
				return err
			}
			g, err := parseRawGeometry(v)
			if err != nil {
				return err
			}
			p.rawGeom = g
		case "threads":
			v, err := next()
			if err != nil {
				return err
			}
			n, err := parseThreads(v)
			if err != nil {
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithEncodeConcurrency(n))
		case "quiet":
			p.quiet = true
		case "h", "help":
			return errHelp
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if p.input == "" || p.output == "" {
		return fmt.Errorf("required parameters are missing\n" +
			"Example: gopj-compress -i image.pgm -o image.j2k")
	}
	// opj_compress rejects any two of -r/-q/-f at once (it XORs the three
	// allocation flags); accepting both and silently letting -q win produced a
	// codestream the user did not ask for (C33).
	if p.haveRates && p.haveQuality {
		return fmt.Errorf("options -r -q and -f cannot be used together !!")
	}
	// -mct 2 needs a coding matrix; without one the codestream is undecodable.
	// The library rejects it too, but failing here gives the exact flag advice
	// opj_compress's help gives (C15).
	if p.mctMode == 2 && !p.haveMCTMatrix {
		return fmt.Errorf("custom MCT (-mct 2) requires a coding matrix: the \"-m <file>\" option has to be used")
	}

	// Resolve the output format before reading the input: an unusable -o must
	// not cost a full image load, and an unknown extension is an error rather
	// than a silent fallback to J2K (C32).
	format, err := outputFormat(p.output)
	if err != nil {
		return err
	}
	p.opts = append(p.opts, gopenjpeg.WithEncodeFormat(format))

	// Route the library diagnostics to stderr; without a handler a failed
	// encode showed only the bare "invalid encoder parameters" (C35).
	p.opts = append(p.opts,
		gopenjpeg.WithEncodeErrorHandler(func(s string) { fmt.Fprintf(os.Stderr, "[ERROR] %s", s) }),
		gopenjpeg.WithEncodeWarningHandler(func(s string) { fmt.Fprintf(os.Stderr, "[WARNING] %s", s) }),
	)
	if !p.quiet {
		p.opts = append(p.opts,
			gopenjpeg.WithEncodeInfoHandler(func(s string) { fmt.Fprintf(os.Stderr, "[INFO] %s", s) }))
	}

	// Byte parity with opj_compress holds for every reader with -s, but only
	// while the image origin is a whole number of sub-sampled steps: when it is
	// not, the reference tile-data gather reads the component rows contiguously
	// instead of stepping by the reference-grid stride, and the two codestreams
	// carry different samples. Say so rather than let it pass unremarked.
	if (p.subX > 1 && p.offsetX%p.subX != 0) || (p.subY > 1 && p.offsetY%p.subY != 0) {
		fmt.Fprintf(os.Stderr, "[WARNING] image offset -d %d,%d is not a multiple of the "+
			"sub-sampling factors -s %d,%d; the codestream will not be byte-identical to opj_compress "+
			"for this combination\n", p.offsetX, p.offsetY, p.subX, p.subY)
	}

	rp := readerParams{
		offX: p.offsetX, offY: p.offsetY,
		subX: p.subX, subY: p.subY,
		mctMode: p.mctMode,
		warn:    func(s string) { fmt.Fprintf(os.Stderr, "[WARNING] %s", s) },
	}
	img, err := readInput(p.input, p.rawGeom, rp)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	// "If subsampled image is provided, automatically disable MCT" — the
	// opj_compress post-parse rule for raw input.
	if p.rawGeom != nil && p.rawGeom.subsampledMCTOff() {
		p.opts = append(p.opts, gopenjpeg.WithMCT(0))
	}

	// Encode into memory first, then publish atomically: a failed encode used
	// to leave a truncated (0 byte) file behind, destroying whatever was at the
	// output path before (C34).
	var buf bytes.Buffer
	if err := gopenjpeg.Encode(img, &buf, p.opts...); err != nil {
		return err
	}
	if err := writeOutputAtomically(p.output, buf.Bytes()); err != nil {
		return err
	}
	if !p.quiet {
		fmt.Fprintf(os.Stderr, "[INFO] Generated outfile %s\n", p.output)
	}
	return nil
}

// outputFormat maps the -o extension to a container, mirroring opj_compress's
// get_file_format for the compressed side. An unrecognised extension is an
// error there and is one here too.
func outputFormat(path string) (gopenjpeg.Format, error) {
	switch strings.ToLower(extOf(path)) {
	case "j2k", "j2c", "jpc":
		return gopenjpeg.FormatJ2K, nil
	case "jp2", "jph":
		return gopenjpeg.FormatJP2, nil
	default:
		return gopenjpeg.FormatJ2K, fmt.Errorf(
			"unknown output format image %s [only *.j2k, *.j2c, *.jpc, *.jp2 or *.jph]", path)
	}
}

// writeOutputAtomically writes data to a temporary file next to path and
// renames it over path, so the destination is either the previous file or the
// complete new one — never a truncated stub.
func writeOutputAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	remove := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		remove()
		return err
	}
	if err := tmp.Close(); err != nil {
		remove()
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		remove()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		remove()
		return err
	}
	return nil
}

// extOf returns the file extension without the leading dot. It uses
// filepath.Ext semantics, so a dot in a *directory* name no longer decides the
// format of an extension-less file (C32).
func extOf(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}
