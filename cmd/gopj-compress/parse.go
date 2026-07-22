package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	gopenjpeg "github.com/mgilbir/gopenjpeg"
)

// parseFloatList parses a comma-separated float list (the -r / -q argument).
func parseFloatList(s string) ([]float32, error) {
	parts := strings.Split(s, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, err
		}
		out = append(out, float32(f))
	}
	return out, nil
}

// parsePair parses "a,b" into two ints.
func parsePair(s string) (int, int, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected a,b got %q", s)
	}
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// parsePrecincts parses the -c argument "[w,h],[w,h],..." into (w,h) pairs.
func parsePrecincts(s string) ([][2]int, error) {
	var out [][2]int
	for _, tok := range strings.Split(s, "],") {
		tok = strings.TrimLeft(tok, "[")
		tok = strings.TrimRight(tok, "]")
		if tok == "" {
			continue
		}
		w, h, err := parsePair(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, [2]int{w, h})
	}
	return out, nil
}

// parseProgression maps a 4-letter progression name to a ProgressionOrder.
func parseProgression(s string) (gopenjpeg.ProgressionOrder, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "LRCP":
		return gopenjpeg.ProgLRCP, nil
	case "RLCP":
		return gopenjpeg.ProgRLCP, nil
	case "RPCL":
		return gopenjpeg.ProgRPCL, nil
	case "PCRL":
		return gopenjpeg.ProgPCRL, nil
	case "CPRL":
		return gopenjpeg.ProgCPRL, nil
	default:
		return gopenjpeg.ProgLRCP, fmt.Errorf("unknown progression order %q", s)
	}
}

// parseROI parses the -ROI argument "c=<compno>,U=<shift>".
func parseROI(s string) (int, int, error) {
	var compno, shift int
	for _, tok := range strings.Split(s, ",") {
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 {
			return 0, 0, fmt.Errorf("bad -ROI token %q", tok)
		}
		v, err := strconv.Atoi(kv[1])
		if err != nil {
			return 0, 0, err
		}
		switch kv[0] {
		case "c":
			compno = v
		case "U":
			shift = v
		}
	}
	return compno, shift, nil
}

// parsePOC parses the -POC argument "T<tile>=r0,c0,l1,r1,c1,ORDER/...".
func parsePOC(s string) ([]gopenjpeg.POCChange, error) {
	var out []gopenjpeg.POCChange
	for len(s) > 0 {
		var tile, r0, c0, l1, r1, c1 uint32
		var order string
		n, err := fmt.Sscanf(s, "T%d=%d,%d,%d,%d,%d,%4s", &tile, &r0, &c0, &l1, &r1, &c1, &order)
		if n != 7 || err != nil {
			break
		}
		prg, perr := parseProgression(order)
		if perr != nil {
			return nil, perr
		}
		out = append(out, gopenjpeg.POCChange{
			Tile: tile, ResStart: r0, CompStart: c0, LayEnd: l1, ResEnd: r1, CompEnd: c1, Order: prg,
		})
		// advance past this record
		idx := strings.Index(s, order)
		if idx < 0 {
			break
		}
		s = s[idx+len(order):]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bad -POC argument %q", s)
	}
	return out, nil
}

// parseMCTFile ports the -m matrix-file parsing of opj_compress: whitespace-
// separated floats, n*n matrix followed by n int DC shifts, where n is derived
// from the total count via n = floor(sqrt(4*count+1)/2 - 0.5).
func parseMCTFile(path string) ([]float32, []int32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	fields := strings.Fields(string(data))
	count := len(fields)
	// n^2 + n = count  =>  n = (sqrt(4*count+1) - 1) / 2
	n := int((sqrtF(float64(4*count+1)))/2.0 - 0.5)
	if n < 1 || n*n+n != count {
		return nil, nil, fmt.Errorf("bad MCT matrix file: %d values", count)
	}
	matrix := make([]float32, n*n)
	for i := 0; i < n*n; i++ {
		f, err := strconv.ParseFloat(fields[i], 32)
		if err != nil {
			return nil, nil, err
		}
		matrix[i] = float32(f)
	}
	dc := make([]int32, n)
	for i := 0; i < n; i++ {
		v, err := strconv.Atoi(fields[n*n+i])
		if err != nil {
			return nil, nil, err
		}
		dc[i] = int32(v)
	}
	return matrix, dc, nil
}

// sqrtF is a small integer-domain sqrt helper (avoids importing math for one use
// path elsewhere; here we just call math via a tiny Newton iteration).
func sqrtF(x float64) float64 {
	if x <= 0 {
		return 0
	}
	g := x
	for i := 0; i < 40; i++ {
		g = 0.5 * (g + x/g)
	}
	return g
}

// parseRawGeometry ports the -F argument
// "width,height,ncomp,bitdepth,{s|u}[@dx1xdy1:dx2xdy2:...]".
func parseRawGeometry(s string) (*rawGeometry, error) {
	head := s
	var subs string
	if i := strings.IndexByte(s, '@'); i >= 0 {
		head = s[:i]
		subs = s[i+1:]
	}
	parts := strings.Split(head, ",")
	if len(parts) != 5 {
		return nil, fmt.Errorf("bad -F argument %q", s)
	}
	toInt := func(x string) (int, error) { return strconv.Atoi(strings.TrimSpace(x)) }
	w, err := toInt(parts[0])
	if err != nil {
		return nil, err
	}
	h, err := toInt(parts[1])
	if err != nil {
		return nil, err
	}
	ncomp, err := toInt(parts[2])
	if err != nil {
		return nil, err
	}
	bd, err := toInt(parts[3])
	if err != nil {
		return nil, err
	}
	signed := false
	switch strings.TrimSpace(parts[4]) {
	case "s":
		signed = true
	case "u":
		signed = false
	default:
		return nil, fmt.Errorf("bad -F sign %q", parts[4])
	}
	g := &rawGeometry{width: w, height: h, numcomps: ncomp, bitdepth: bd, signed: signed}
	g.dx = make([]int, ncomp)
	g.dy = make([]int, ncomp)
	lastdx, lastdy := 1, 1
	specs := strings.Split(subs, ":")
	for c := 0; c < ncomp; c++ {
		if subs != "" && c < len(specs) && specs[c] != "" {
			var dx, dy int
			if _, err := fmt.Sscanf(specs[c], "%dx%d", &dx, &dy); err != nil {
				return nil, fmt.Errorf("bad -F subsampling %q", specs[c])
			}
			lastdx, lastdy = dx, dy
		}
		g.dx[c] = lastdx
		g.dy[c] = lastdy
	}
	return g, nil
}

// parseIMF ports the -IMF argument
// "<PROFILE>[,mainlevel=X][,sublevel=Y][,framerate=FPS]" into an rsiz value.
func parseIMF(s string) (uint16, error) {
	parts := strings.Split(s, ",")
	var profile uint16
	switch strings.TrimSpace(parts[0]) {
	case "2K":
		profile = 0x0400
	case "4K":
		profile = 0x0500
	case "8K":
		profile = 0x0600
	case "2K_R":
		profile = 0x0700
	case "4K_R":
		profile = 0x0800
	case "8K_R":
		profile = 0x0900
	default:
		return 0, fmt.Errorf("bad -IMF profile %q", parts[0])
	}
	mainlevel, sublevel := 0, 0
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		switch {
		case strings.HasPrefix(p, "mainlevel="):
			v, err := strconv.Atoi(p[len("mainlevel="):])
			if err != nil || v < 0 || v > 15 {
				return 0, fmt.Errorf("bad IMF mainlevel")
			}
			mainlevel = v
		case strings.HasPrefix(p, "sublevel="):
			v, err := strconv.Atoi(p[len("sublevel="):])
			if err != nil || v < 0 || v > 15 {
				return 0, fmt.Errorf("bad IMF sublevel")
			}
			sublevel = v
		case strings.HasPrefix(p, "framerate="):
			// framerate only affects checks/max-rate; ignored for codestream parity.
		}
	}
	return profile | uint16(sublevel<<4) | uint16(mainlevel), nil
}
