// Command gopj-compress encodes a raster image into a JPEG 2000 codestream
// (.j2k/.j2c) or JP2 container (.jp2), mirroring the flags and defaults of the
// reference opj_compress tool closely enough that the produced codestream is
// byte-identical for the supported cases.
//
// Supported input formats (chosen by extension): PGM/PPM/PNM (netpbm),
// PGX, and RAW/RAWL (headerless, geometry supplied with -F). Output format is
// chosen by the -o extension (.j2k/.j2c/.jpc or .jp2/.jph).
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR -> gopj-compress: %v\n", err)
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
	quiet            bool
}

func run(args []string) error {
	p := cliParams{mctMode: -1}

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
				return err
			}
			p.opts = append(p.opts, gopenjpeg.WithSubsampling(dx, dy))
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
			if len(v) != 1 {
				return fmt.Errorf("bad -TP value %q", v)
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
		case "quiet":
			p.quiet = true
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if p.input == "" || p.output == "" {
		return fmt.Errorf("both -i and -o are required")
	}

	img, err := readInput(p.input, p.rawGeom, p.mctMode, p.offsetX, p.offsetY)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	format := gopenjpeg.FormatJ2K
	switch strings.ToLower(extOf(p.output)) {
	case "jp2", "jph":
		format = gopenjpeg.FormatJP2
	}
	p.opts = append(p.opts, gopenjpeg.WithEncodeFormat(format))

	f, err := os.Create(p.output)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := gopenjpeg.Encode(img, f, p.opts...); err != nil {
		return err
	}
	return nil
}

func extOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[i+1:]
}
