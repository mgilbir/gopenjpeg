package dwt

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func (r *binReader) f64() float64 {
	v := binary.LittleEndian.Uint64(r.b[r.pos:])
	r.pos += 8
	return math.Float64frombits(v)
}

// TestNormsVectors checks getnorm/getnorm_real and calc_explicit_stepsizes
// against the C oracle.
func TestNormsVectors(t *testing.T) {
	data, err := os.ReadFile(vectorPath("norms.bin"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	r := &binReader{b: data}

	levels := r.u32()
	orients := r.u32()
	for level := uint32(0); level < levels; level++ {
		for orient := uint32(0); orient < orients; orient++ {
			wantN := r.f64()
			wantNR := r.f64()
			if got := Getnorm(level, orient); got != wantN {
				t.Errorf("Getnorm(%d,%d)=%v want %v", level, orient, got, wantN)
			}
			if got := GetnormReal(level, orient); got != wantNR {
				t.Errorf("GetnormReal(%d,%d)=%v want %v", level, orient, got, wantNR)
			}
		}
	}

	ncfg := r.u32()
	for ci := uint32(0); ci < ncfg; ci++ {
		numres := r.u32()
		qmfbid := r.u32()
		qntsty := r.u32()
		prec := r.u32()
		numbands := r.u32()
		tccp := &Tccp{
			Numresolutions: numres,
			Qmfbid:         qmfbid,
			Qntsty:         qntsty,
			Stepsizes:      make([]Stepsize, numbands),
		}
		CalcExplicitStepsizes(tccp, prec)
		for bn := uint32(0); bn < numbands; bn++ {
			wantExpn := r.i32()
			wantMant := r.i32()
			if tccp.Stepsizes[bn].Expn != wantExpn || tccp.Stepsizes[bn].Mant != wantMant {
				t.Fatalf("cfg %d (numres=%d qmfbid=%d qntsty=%d prec=%d) band %d: got {%d,%d} want {%d,%d}",
					ci, numres, qmfbid, qntsty, prec, bn,
					tccp.Stepsizes[bn].Expn, tccp.Stepsizes[bn].Mant, wantExpn, wantMant)
			}
		}
	}
	if r.pos != len(data) {
		t.Fatalf("trailing bytes: pos=%d len=%d", r.pos, len(data))
	}
}
