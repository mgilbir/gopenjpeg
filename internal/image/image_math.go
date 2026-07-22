package image

// This file holds the small integer-geometry helpers needed by the image
// header-update logic, ported from opj_intmath.h. They are duplicated here so
// this package stays independent of other in-flight workers.
//
// TODO: switch to internal/opjmath once that package lands.

// uintMin is a port of opj_uint_min.
func uintMin(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// uintMax is a port of opj_uint_max.
func uintMax(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// uintAdds is a port of opj_uint_adds: saturated unsigned addition.
func uintAdds(a, b uint32) uint32 {
	sum := uint64(a) + uint64(b)
	return uint32(-int32(sum>>32)) | uint32(sum)
}

// uintCeildiv is a port of opj_uint_ceildiv: divide and round upwards.
func uintCeildiv(a, b uint32) uint32 {
	return uint32((uint64(a) + uint64(b) - 1) / uint64(b))
}

// uintCeildivpow2 is a port of opj_uint_ceildivpow2: divide by 2^b, round up.
func uintCeildivpow2(a, b uint32) uint32 {
	return uint32((uint64(a) + (uint64(1) << b) - 1) >> b)
}
