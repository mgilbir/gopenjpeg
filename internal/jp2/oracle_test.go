package jp2

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/mgilbir/gopenjpeg/oracletest"
)

var (
	reX1  = regexp.MustCompile(`x1=(\d+),\s*y1=(\d+)`)
	reNum = regexp.MustCompile(`numcomps=(\d+)`)
)

// TestOracleIHDRConsistency cross-checks the JP2 ihdr geometry our parser
// extracts against opj_dump's reported image bounds and component count for the
// checked-in files. It is skipped when the oracle is not present. Files whose
// ihdr width/height intentionally disagree with the codestream SIZ (or that
// opj_dump cannot read) are skipped individually.
func TestOracleIHDRConsistency(t *testing.T) {
	oracletest.Require(t)
	golden := loadGolden(t)

	for name, want := range golden {
		t.Run(name, func(t *testing.T) {
			fp := filepath.Join(filesDir, name)
			out, err := exec.Command(oracletest.Bin("opj_dump"), "-i", fp).CombinedOutput()
			if err != nil {
				t.Skipf("opj_dump could not read %s: %v", name, err)
			}
			mx := reX1.FindSubmatch(out)
			mn := reNum.FindSubmatch(out)
			if mx == nil || mn == nil {
				t.Skipf("opj_dump produced no Image info for %s", name)
			}
			x1 := atoi(t, mx[1])
			y1 := atoi(t, mx[2])
			nc := atoi(t, mn[1])

			// golden IHDR = [h, w, nc, ...]; opj_dump x1=Xsiz=w, y1=Ysiz=h.
			wantH, wantW, wantNC := want.IHDR[0], want.IHDR[1], want.IHDR[2]
			if x1 != wantW || y1 != wantH {
				t.Errorf("%s: ihdr geometry w,h=(%d,%d) but opj_dump x1,y1=(%d,%d)",
					name, wantW, wantH, x1, y1)
			}
			if nc != wantNC {
				t.Errorf("%s: ihdr numcomps=%d but opj_dump numcomps=%d", name, wantNC, nc)
			}
		})
	}
}

func atoi(t *testing.T, b []byte) uint32 {
	t.Helper()
	v, err := strconv.ParseUint(string(b), 10, 32)
	if err != nil {
		t.Fatalf("parse %q: %v", b, err)
	}
	return uint32(v)
}
