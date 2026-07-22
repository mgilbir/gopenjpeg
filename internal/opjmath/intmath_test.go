package opjmath

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func pI32(t *testing.T, s string) int32 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		t.Fatalf("parse int32 %q: %v", s, err)
	}
	return int32(v)
}

func pU32(t *testing.T, s string) uint32 {
	t.Helper()
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		t.Fatalf("parse uint32 %q: %v", s, err)
	}
	return uint32(v)
}

func pI64(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parse int64 %q: %v", s, err)
	}
	return v
}

func pU64(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("parse uint64 %q: %v", s, err)
	}
	return v
}

// TestOracleVectors replays every line of the C-generated intmath.txt vector
// file and checks that the Go ports produce identical results.
func TestOracleVectors(t *testing.T) {
	f, err := os.Open("../../testdata/vectors/opjmath/intmath.txt")
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		txt := strings.TrimSpace(sc.Text())
		if txt == "" {
			continue
		}
		fld := strings.Fields(txt)
		name := fld[0]
		a := fld[1:]

		switch name {
		case "int_min":
			got := IntMin(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_max":
			got := IntMax(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_fix_mul":
			got := IntFixMul(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_fix_mul_t1":
			got := IntFixMulT1(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_add_no_overflow":
			got := IntAddNoOverflow(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_sub_no_overflow":
			got := IntSubNoOverflow(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_ceildiv":
			got := IntCeildiv(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_clamp":
			got := IntClamp(pI32(t, a[0]), pI32(t, a[1]), pI32(t, a[2]))
			checkI32(t, line, name, got, pI32(t, a[3]))
		case "int64_clamp":
			got := Int64Clamp(pI64(t, a[0]), pI64(t, a[1]), pI64(t, a[2]))
			checkI64(t, line, name, got, pI64(t, a[3]))
		case "int_abs":
			got := IntAbs(pI32(t, a[0]))
			checkI32(t, line, name, got, pI32(t, a[1]))
		case "int_floorlog2":
			got := IntFloorlog2(pI32(t, a[0]))
			checkI32(t, line, name, got, pI32(t, a[1]))
		case "int_ceildivpow2":
			got := IntCeildivpow2(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int_floordivpow2":
			got := IntFloordivpow2(pI32(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "int64_ceildivpow2":
			got := Int64Ceildivpow2(pI64(t, a[0]), pI32(t, a[1]))
			checkI32(t, line, name, got, pI32(t, a[2]))
		case "uint_min":
			got := UintMin(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_max":
			got := UintMax(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_adds":
			got := UintAdds(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_subs":
			got := UintSubs(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_ceildiv":
			got := UintCeildiv(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_ceildivpow2":
			got := UintCeildivpow2(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_floordivpow2":
			got := UintFloordivpow2(pU32(t, a[0]), pU32(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		case "uint_floorlog2":
			got := UintFloorlog2(pU32(t, a[0]))
			checkU32(t, line, name, got, pU32(t, a[1]))
		case "uint64_ceildiv_res_uint32":
			got := Uint64CeildivResUint32(pU64(t, a[0]), pU64(t, a[1]))
			checkU32(t, line, name, got, pU32(t, a[2]))
		default:
			t.Fatalf("line %d: unknown function %q", line, name)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if line == 0 {
		t.Fatal("no vectors read")
	}
}

func checkI32(t *testing.T, line int, name string, got, want int32) {
	t.Helper()
	if got != want {
		t.Errorf("line %d %s: got %d want %d", line, name, got, want)
	}
}

func checkI64(t *testing.T, line int, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("line %d %s: got %d want %d", line, name, got, want)
	}
}

func checkU32(t *testing.T, line int, name string, got, want uint32) {
	t.Helper()
	if got != want {
		t.Errorf("line %d %s: got %d want %d", line, name, got, want)
	}
}

// TestEdgeCases documents a few tricky semantics directly, independent of the
// oracle file.
func TestEdgeCases(t *testing.T) {
	// IntAbs(MinInt32) overflows to itself, matching C.
	if got := IntAbs(math.MinInt32); got != math.MinInt32 {
		t.Errorf("IntAbs(MinInt32) = %d, want %d", got, int32(math.MinInt32))
	}
	// UintAdds saturates.
	if got := UintAdds(0xFFFFFFFF, 1); got != 0xFFFFFFFF {
		t.Errorf("UintAdds overflow = %#x, want 0xFFFFFFFF", got)
	}
	if got := UintAdds(0x10, 0x20); got != 0x30 {
		t.Errorf("UintAdds normal = %#x, want 0x30", got)
	}
	// UintSubs clamps at 0.
	if got := UintSubs(3, 10); got != 0 {
		t.Errorf("UintSubs underflow = %d, want 0", got)
	}
	// floorlog2 of non-positive is 0.
	if got := IntFloorlog2(0); got != 0 {
		t.Errorf("IntFloorlog2(0) = %d, want 0", got)
	}
	if got := IntFloorlog2(-5); got != 0 {
		t.Errorf("IntFloorlog2(-5) = %d, want 0", got)
	}
	if got := UintFloorlog2(1024); got != 10 {
		t.Errorf("UintFloorlog2(1024) = %d, want 10", got)
	}
	// Two's-complement wraparound helpers.
	if got := IntAddNoOverflow(math.MaxInt32, 1); got != math.MinInt32 {
		t.Errorf("IntAddNoOverflow overflow = %d, want %d", got, int32(math.MinInt32))
	}
}
