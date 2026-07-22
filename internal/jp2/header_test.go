package jp2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// headerFacts is the compact set of JP2-box-level header facts extracted from a
// file, checked in as golden data under testdata/vectors/jp2/golden.json. Every
// field is produced purely from the JP2 container boxes (no codestream decode).
type headerFacts struct {
	IHDR            [7]uint32 `json:"ihdr"`           // h, w, nc, bpc, C, UnkC, IPR
	CompBpcc        []uint32  `json:"comp_bpcc"`      // per-component bpcc (0 unless a BPCC box was read)
	Colr            []uint32  `json:"colr,omitempty"` // meth, precedence, approx, enumcs
	HasColr         bool      `json:"has_colr"`       // jp2_has_colr
	ICCLen          uint32    `json:"icc_len"`        // ICC profile length (0 for enumerated / CIELab)
	ICCBufBytes     int       `json:"icc_buf_bytes"`  // len of captured ICC/CIELab buffer
	Pclr            []uint32  `json:"pclr,omitempty"` // ne, npc
	CmapN           uint32    `json:"cmap_n"`         // number of cmap channels
	CdefN           uint32    `json:"cdef_n"`         // number of cdef entries
	ImgStateUnknown bool      `json:"img_state_unknown"`
}

// extractFacts parses fp as a JP2 file up to (not including) the codestream and
// returns the header facts. It drives readHeaderProcedure directly, so no codec
// decode is involved (the stub only records the ihdr callbacks).
func extractFacts(t *testing.T, fp string) headerFacts {
	t.Helper()
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read %s: %v", fp, err)
	}
	jp2, _ := newTestJP2()
	stream := cio.NewMemoryInputStream(data)
	mgr := &event.Manager{} // silent
	if !jp2.readHeaderProcedure(stream, mgr) {
		t.Fatalf("readHeaderProcedure failed for %s", fp)
	}
	return factsFromJP2(jp2)
}

func factsFromJP2(jp2 *JP2) headerFacts {
	f := headerFacts{
		IHDR:            [7]uint32{jp2.h, jp2.w, jp2.numcomps, jp2.bpc, jp2.c, jp2.unkC, jp2.ipr},
		HasColr:         jp2.color.JP2HasColr != 0,
		ICCLen:          jp2.color.ICCProfileLen,
		ICCBufBytes:     len(jp2.color.ICCProfileBuf),
		Colr:            []uint32{jp2.meth, jp2.precedence, jp2.approx, jp2.enumcs},
		ImgStateUnknown: jp2.jp2ImgState&imgStateUnknown != 0,
	}
	for _, c := range jp2.comps {
		f.CompBpcc = append(f.CompBpcc, c.Bpcc)
	}
	if jp2.color.Pclr != nil {
		f.Pclr = []uint32{uint32(jp2.color.Pclr.NrEntries), uint32(jp2.color.Pclr.NrChannels)}
		if jp2.color.Pclr.Cmap != nil {
			f.CmapN = uint32(len(jp2.color.Pclr.Cmap))
		}
	}
	if jp2.color.Cdef != nil {
		f.CdefN = uint32(jp2.color.Cdef.N)
	}
	return f
}

const goldenPath = "../../testdata/vectors/jp2/golden.json"
const filesDir = "../../testdata/vectors/jp2/files"

// TestHeaderGolden parses every checked-in JP2 file and compares the extracted
// header facts against the golden JSON. It runs fully offline (no oracle).
func TestHeaderGolden(t *testing.T) {
	golden := loadGolden(t)
	for name, want := range golden {
		t.Run(name, func(t *testing.T) {
			got := extractFacts(t, filepath.Join(filesDir, name))
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Errorf("facts mismatch for %s\n got: %s\nwant: %s", name, gb, wb)
			}
		})
	}
}

func loadGolden(t *testing.T) map[string]headerFacts {
	t.Helper()
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var m map[string]headerFacts
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	return m
}

// TestGenerateGolden regenerates testdata/vectors/jp2/golden.json from the
// checked-in files. It is skipped unless GOPENJPEG_GEN_GOLDEN=1 is set.
//
//	GOPENJPEG_GEN_GOLDEN=1 go test ./internal/jp2 -run TestGenerateGolden
func TestGenerateGolden(t *testing.T) {
	if os.Getenv("GOPENJPEG_GEN_GOLDEN") != "1" {
		t.Skip("set GOPENJPEG_GEN_GOLDEN=1 to regenerate golden.json")
	}
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]headerFacts{}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jp2" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		m[name] = extractFacts(t, filepath.Join(filesDir, name))
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(goldenPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d entries to %s", len(m), goldenPath)
}
