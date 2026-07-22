// Package opjmath ports the integer-math helpers from opj_intmath.h. Every
// function reproduces the exact integer widths, intermediate promotions,
// rounding and two's-complement wraparound of the C originals. Where C uses
// OPJ_INT32/OPJ_UINT32/OPJ_INT64/OPJ_UINT64 the Go code uses
// int32/uint32/int64/uint64 accordingly.
package opjmath

// t1NMSEDECFracBits ports T1_NMSEDEC_FRACBITS from t1.h, defined as
// T1_NMSEDEC_BITS-1 with T1_NMSEDEC_BITS == 7, i.e. 6. It is used by
// IntFixMulT1's shift amount.
const t1NMSEDECFracBits = 6

// IntMin ports opj_int_min: minimum of two signed 32-bit integers.
func IntMin(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// UintMin ports opj_uint_min: minimum of two unsigned 32-bit integers.
func UintMin(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// IntMax ports opj_int_max: maximum of two signed 32-bit integers.
func IntMax(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// UintMax ports opj_uint_max: maximum of two unsigned 32-bit integers.
func UintMax(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// UintAdds ports opj_uint_adds: the saturated (clamped to UINT32_MAX) sum of
// two unsigned 32-bit integers. The C code computes a 64-bit sum then uses
// (OPJ_UINT32)(-(OPJ_INT32)(sum>>32)) | (OPJ_UINT32)sum to fold any carry into
// an all-ones mask. This reproduction keeps that exact bit-twiddling so the
// wraparound semantics match precisely.
func UintAdds(a, b uint32) uint32 {
	sum := uint64(a) + uint64(b)
	// -(int32)(sum>>32): when there is a carry (sum>>32 == 1) this is
	// (uint32)(-1) == 0xFFFFFFFF; otherwise 0. OR-ing with the low 32 bits
	// yields 0xFFFFFFFF on overflow, else a+b.
	return uint32(-int32(sum>>32)) | uint32(sum)
}

// UintSubs ports opj_uint_subs: the saturated difference a-b, clamped at 0.
func UintSubs(a, b uint32) uint32 {
	if a >= b {
		return a - b
	}
	return 0
}

// IntClamp ports opj_int_clamp: clamp a signed 32-bit value into [min, max].
func IntClamp(a, min, max int32) int32 {
	if a < min {
		return min
	}
	if a > max {
		return max
	}
	return a
}

// Int64Clamp ports opj_int64_clamp: clamp a signed 64-bit value into [min, max].
func Int64Clamp(a, min, max int64) int64 {
	if a < min {
		return min
	}
	if a > max {
		return max
	}
	return a
}

// IntAbs ports opj_int_abs: absolute value of a signed 32-bit integer.
// Note: like the C code, IntAbs(math.MinInt32) returns math.MinInt32 because
// -a overflows (two's-complement wraparound), which is intentional.
func IntAbs(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

// IntCeildiv ports opj_int_ceildiv: divide a by b rounding upwards, computing
// the numerator in 64 bits to match ((OPJ_INT64)a + b - 1) / b, then narrowing
// to int32. The C code asserts b != 0; callers must not pass b == 0 (Go would
// panic on integer divide-by-zero, matching the effect of the C assert).
func IntCeildiv(a, b int32) int32 {
	return int32((int64(a) + int64(b) - 1) / int64(b))
}

// UintCeildiv ports opj_uint_ceildiv: unsigned divide rounding upwards with a
// 64-bit intermediate numerator, narrowed to uint32.
func UintCeildiv(a, b uint32) uint32 {
	return uint32((uint64(a) + uint64(b) - 1) / uint64(b))
}

// Uint64CeildivResUint32 ports opj_uint64_ceildiv_res_uint32: divide two
// unsigned 64-bit values rounding upwards, returning a uint32.
func Uint64CeildivResUint32(a, b uint64) uint32 {
	return uint32((a + b - 1) / b)
}

// IntCeildivpow2 ports opj_int_ceildivpow2: divide a by 2^b rounding upwards.
// The addend is formed in 64 bits ((OPJ_INT64)1 << b) and the arithmetic right
// shift is performed on the 64-bit sum before narrowing to int32.
func IntCeildivpow2(a, b int32) int32 {
	return int32((int64(a) + (int64(1) << uint(b)) - 1) >> uint(b))
}

// Int64Ceildivpow2 ports opj_int64_ceildivpow2: divide a 64-bit a by 2^b
// rounding upwards, returning int32.
func Int64Ceildivpow2(a int64, b int32) int32 {
	return int32((a + (int64(1) << uint(b)) - 1) >> uint(b))
}

// UintCeildivpow2 ports opj_uint_ceildivpow2: unsigned divide by 2^b rounding
// upwards, with the addend and shift performed in 64 bits.
func UintCeildivpow2(a, b uint32) uint32 {
	return uint32((uint64(a) + (uint64(1) << uint(b)) - 1) >> uint(b))
}

// IntFloordivpow2 ports opj_int_floordivpow2: arithmetic right shift a>>b.
func IntFloordivpow2(a, b int32) int32 {
	return a >> uint(b)
}

// UintFloordivpow2 ports opj_uint_floordivpow2: logical right shift a>>b.
func UintFloordivpow2(a, b uint32) uint32 {
	return a >> uint(b)
}

// IntFloorlog2 ports opj_int_floorlog2: floor(log2(a)) via the same
// shift-until-<=1 loop as C. For a <= 0 the loop body never runs (a>1 false),
// returning 0, exactly as the C loop does.
func IntFloorlog2(a int32) int32 {
	var l int32
	for ; a > 1; l++ {
		a >>= 1
	}
	return l
}

// UintFloorlog2 ports opj_uint_floorlog2: floor(log2(a)) for unsigned input.
func UintFloorlog2(a uint32) uint32 {
	var l uint32
	for ; a > 1; l++ {
		a >>= 1
	}
	return l
}

// IntFixMul ports opj_int_fix_mul: multiply two fixed-point values with a
// 64-bit intermediate, adding 4096 for rounding and shifting right by 13.
// The C asserts guard against the >>13 result exceeding int32 range; those are
// debug-only and are not reproduced (release C does not check them), but the
// arithmetic — 64-bit multiply, +4096, arithmetic >>13, narrow to int32 —
// matches exactly.
func IntFixMul(a, b int32) int32 {
	temp := int64(a) * int64(b)
	temp += 4096
	return int32(temp >> 13)
}

// IntFixMulT1 ports opj_int_fix_mul_t1: like IntFixMul but shifting by
// 13 + 11 - T1_NMSEDEC_FRACBITS == 13 + 11 - 6 == 18.
func IntFixMulT1(a, b int32) int32 {
	temp := int64(a) * int64(b)
	temp += 4096
	return int32(temp >> (13 + 11 - t1NMSEDECFracBits))
}

// IntAddNoOverflow ports opj_int_add_no_overflow: add two signed 32-bit
// integers with defined two's-complement wraparound (the C code type-puns
// through uint32 to avoid signed-overflow UB).
func IntAddNoOverflow(a, b int32) int32 {
	return int32(uint32(a) + uint32(b))
}

// IntSubNoOverflow ports opj_int_sub_no_overflow: subtract two signed 32-bit
// integers with defined two's-complement wraparound.
func IntSubNoOverflow(a, b int32) int32 {
	return int32(uint32(a) - uint32(b))
}
