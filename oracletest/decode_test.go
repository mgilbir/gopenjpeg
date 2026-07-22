package oracletest

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
)

// pgx holds one component decoded by opj_decompress.
type pgx struct {
	w, h   int
	signed bool
	depth  int
	data   []int32
}

// readPGX parses a PGX file produced by opj_decompress.
// Header: "PG <ML|LM> <+|-> <depth> <width> <height>\n" then raw samples.
func readPGX(path string) (*pgx, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	header, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) < 6 || fields[0] != "PG" {
		return nil, fmt.Errorf("bad PGX header: %q", header)
	}
	bigEndian := fields[1] == "ML"
	signed := fields[2] == "-"
	depth, _ := strconv.Atoi(fields[3])
	w, _ := strconv.Atoi(fields[4])
	h, _ := strconv.Atoi(fields[5])
	nbytes := (depth + 7) / 8

	n := w * h
	data := make([]int32, n)
	raw := make([]byte, nbytes)
	for i := 0; i < n; i++ {
		if _, err := readFull(br, raw); err != nil {
			return nil, fmt.Errorf("pgx short read at sample %d: %w", i, err)
		}
		var v uint32
		if bigEndian {
			for _, b := range raw {
				v = (v << 8) | uint32(b)
			}
		} else {
			for k := nbytes - 1; k >= 0; k-- {
				v = (v << 8) | uint32(raw[k])
			}
		}
		if signed {
			shift := 32 - nbytes*8
			data[i] = int32(v<<shift) >> shift
		} else {
			data[i] = int32(v)
		}
	}
	return &pgx{w: w, h: h, signed: signed, depth: depth, data: data}, nil
}

func readFull(br *bufio.Reader, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		m, err := br.Read(buf[got:])
		got += m
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

var _ = binary.BigEndian

// runOracleAllowFail runs opj_decompress but returns the error instead of
// failing the test (so we can assert Go errors when the oracle also errors).
func runOracleAllowFail(input string, args ...string) ([]byte, error) {
	_ = input
	return execOracle("opj_decompress", args...)
}

// itoa formats a non-negative int without importing strconv at call sites.
func itoa(i int) string { return strconv.Itoa(i) }

// decodeGo decodes a raw codestream with the pure-Go j2k decoder.
func decodeGo(t *testing.T, path string, reduce, layer uint32, area *[4]int32) (*image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := cio.NewMemoryInputStream(data)
	// NOTE: a nil event manager is used deliberately. internal/t2's warnf helper
	// (t2.go) recurses infinitely when given a non-nil manager (a W6 bug outside
	// W7's edit scope); a nil manager is nil-safe throughout j2k/tcd/t2. See the
	// worker report's integration notes.
	var mgr *event.Manager
	d := j2k.CreateDecompress()
	d.SetupDecoder(reduce, layer)
	d.SetStrictMode(false)
	img, err := d.ReadHeader(s, mgr)
	if err != nil {
		return nil, fmt.Errorf("ReadHeader: %w", err)
	}
	var aerr error
	if area != nil {
		aerr = d.SetDecodeArea(img, area[0], area[1], area[2], area[3])
	} else {
		aerr = d.SetDecodeArea(img, 0, 0, 0, 0)
	}
	if aerr != nil {
		return nil, fmt.Errorf("SetDecodeArea: %w", aerr)
	}
	if err := d.Decode(s, img, mgr); err != nil {
		return nil, fmt.Errorf("Decode: %w", err)
	}
	return img, nil
}

// oracleDecodePGX runs opj_decompress and returns one pgx per component.
func oracleDecodePGX(t *testing.T, input string, extraArgs ...string) ([]*pgx, error) {
	dir := t.TempDir()
	base := filepath.Join(dir, "out.pgx")
	args := append([]string{"-i", input, "-o", base}, extraArgs...)
	out, err := runOracleAllowFail(input, args...)
	if err != nil {
		return nil, fmt.Errorf("opj_decompress failed: %v\n%s", err, out)
	}
	// Collect out_N.pgx in order.
	var comps []*pgx
	for i := 0; ; i++ {
		p := filepath.Join(dir, fmt.Sprintf("out_%d.pgx", i))
		if _, err := os.Stat(p); err != nil {
			break
		}
		c, err := readPGX(p)
		if err != nil {
			return nil, err
		}
		comps = append(comps, c)
	}
	if len(comps) == 0 {
		return nil, fmt.Errorf("opj_decompress produced no PGX components")
	}
	return comps, nil
}

// compareComps compares the Go-decoded image against the oracle PGX components.
func compareComps(t *testing.T, img *image.Image, comps []*pgx) error {
	if int(img.Numcomps) != len(comps) {
		return fmt.Errorf("component count mismatch: go=%d oracle=%d", img.Numcomps, len(comps))
	}
	for i := 0; i < len(comps); i++ {
		gc := &img.Comps[i]
		oc := comps[i]
		if int(gc.W) != oc.w || int(gc.H) != oc.h {
			return fmt.Errorf("comp %d dim mismatch: go=%dx%d oracle=%dx%d", i, gc.W, gc.H, oc.w, oc.h)
		}
		if gc.Data == nil {
			return fmt.Errorf("comp %d: go data is nil", i)
		}
		n := oc.w * oc.h
		if len(gc.Data) < n {
			return fmt.Errorf("comp %d: go data too short %d < %d", i, len(gc.Data), n)
		}
		for k := 0; k < n; k++ {
			if gc.Data[k] != oc.data[k] {
				return fmt.Errorf("comp %d sample %d (x=%d,y=%d) mismatch: go=%d oracle=%d",
					i, k, k%oc.w, k/oc.w, gc.Data[k], oc.data[k])
			}
		}
	}
	return nil
}
