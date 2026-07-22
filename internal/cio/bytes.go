// Package cio ports cio.c/cio.h: the byte-order (de)serialisation helpers and
// the opj_stream_t byte-stream abstraction with C-faithful buffered read/write,
// skip and seek semantics.
package cio

import "math"

// The C helpers come in _BE and _LE variants that are host-endianness
// specialisations (one raw-copies, the other byte-swaps). The cio.h macros
// (opj_write_bytes, opj_read_bytes, opj_write_double, ...) dispatch on
// OPJ_BIG_ENDIAN so that the resulting serialisation is always big-endian, and
// the whole OpenJPEG codebase uses only those macros. WriteBytes/ReadBytes and
// the double/float equivalents below port those macros (big-endian codestream
// order) and are what higher-level packages should use. The explicit *BE / *LE
// functions expose true big- and little-endian serialisation for completeness.

// WriteBytes ports the opj_write_bytes macro (big-endian codestream order).
func WriteBytes(buffer []byte, value uint32, nbBytes uint32) {
	WriteBytesBE(buffer, value, nbBytes)
}

// ReadBytes ports the opj_read_bytes macro (big-endian codestream order).
func ReadBytes(buffer []byte, nbBytes uint32) uint32 {
	return ReadBytesBE(buffer, nbBytes)
}

// WriteDouble ports the opj_write_double macro (big-endian).
func WriteDouble(buffer []byte, value float64) { WriteDoubleBE(buffer, value) }

// ReadDouble ports the opj_read_double macro (big-endian).
func ReadDouble(buffer []byte) float64 { return ReadDoubleBE(buffer) }

// WriteFloat ports the opj_write_float macro (big-endian).
func WriteFloat(buffer []byte, value float32) { WriteFloatBE(buffer, value) }

// ReadFloat ports the opj_read_float macro (big-endian).
func ReadFloat(buffer []byte) float32 { return ReadFloatBE(buffer) }

// WriteBytesBE ports opj_write_bytes_BE: write the low nbBytes of value into
// buffer in big-endian order. nbBytes must be in [1,4]; the C code asserts
// this. It writes exactly nbBytes bytes starting at buffer[0].
func WriteBytesBE(buffer []byte, value uint32, nbBytes uint32) {
	// C copies from (&value)+sizeof(uint32)-nbBytes for nbBytes bytes, i.e.
	// the nbBytes least-significant bytes in big-endian order.
	for i := uint32(0); i < nbBytes; i++ {
		shift := (nbBytes - 1 - i) * 8
		buffer[i] = byte(value >> shift)
	}
}

// WriteBytesLE ports opj_write_bytes_LE: write the low nbBytes of value into
// buffer in little-endian order. nbBytes must be in [1,4].
func WriteBytesLE(buffer []byte, value uint32, nbBytes uint32) {
	for i := uint32(0); i < nbBytes; i++ {
		buffer[i] = byte(value >> (i * 8))
	}
}

// ReadBytesBE ports opj_read_bytes_BE: read nbBytes from buffer as a big-endian
// unsigned integer. The C code zeroes the destination first and fills the low
// nbBytes bytes, so the result is zero-extended. nbBytes must be in [1,4].
func ReadBytesBE(buffer []byte, nbBytes uint32) uint32 {
	var value uint32
	for i := uint32(0); i < nbBytes; i++ {
		value = (value << 8) | uint32(buffer[i])
	}
	return value
}

// ReadBytesLE ports opj_read_bytes_LE: read nbBytes from buffer as a
// little-endian unsigned integer, zero-extended. nbBytes must be in [1,4].
func ReadBytesLE(buffer []byte, nbBytes uint32) uint32 {
	var value uint32
	for i := uint32(0); i < nbBytes; i++ {
		value |= uint32(buffer[i]) << (i * 8)
	}
	return value
}

// WriteDoubleBE ports opj_write_double_BE: write a float64 as 8 big-endian
// bytes (IEEE-754 bit pattern, most significant byte first).
func WriteDoubleBE(buffer []byte, value float64) {
	bits := math.Float64bits(value)
	for i := 0; i < 8; i++ {
		buffer[i] = byte(bits >> (uint(7-i) * 8))
	}
}

// WriteDoubleLE ports opj_write_double_LE: write a float64 as 8 little-endian
// bytes.
func WriteDoubleLE(buffer []byte, value float64) {
	bits := math.Float64bits(value)
	for i := 0; i < 8; i++ {
		buffer[i] = byte(bits >> (uint(i) * 8))
	}
}

// ReadDoubleBE ports opj_read_double_BE: read 8 big-endian bytes as a float64.
func ReadDoubleBE(buffer []byte) float64 {
	var bits uint64
	for i := 0; i < 8; i++ {
		bits = (bits << 8) | uint64(buffer[i])
	}
	return math.Float64frombits(bits)
}

// ReadDoubleLE ports opj_read_double_LE: read 8 little-endian bytes as a
// float64.
func ReadDoubleLE(buffer []byte) float64 {
	var bits uint64
	for i := 0; i < 8; i++ {
		bits |= uint64(buffer[i]) << (uint(i) * 8)
	}
	return math.Float64frombits(bits)
}

// WriteFloatBE ports opj_write_float_BE: write a float32 as 4 big-endian bytes.
func WriteFloatBE(buffer []byte, value float32) {
	bits := math.Float32bits(value)
	for i := 0; i < 4; i++ {
		buffer[i] = byte(bits >> (uint(3-i) * 8))
	}
}

// WriteFloatLE ports opj_write_float_LE: write a float32 as 4 little-endian
// bytes.
func WriteFloatLE(buffer []byte, value float32) {
	bits := math.Float32bits(value)
	for i := 0; i < 4; i++ {
		buffer[i] = byte(bits >> (uint(i) * 8))
	}
}

// ReadFloatBE ports opj_read_float_BE: read 4 big-endian bytes as a float32.
func ReadFloatBE(buffer []byte) float32 {
	var bits uint32
	for i := 0; i < 4; i++ {
		bits = (bits << 8) | uint32(buffer[i])
	}
	return math.Float32frombits(bits)
}

// ReadFloatLE ports opj_read_float_LE: read 4 little-endian bytes as a float32.
func ReadFloatLE(buffer []byte) float32 {
	var bits uint32
	for i := 0; i < 4; i++ {
		bits |= uint32(buffer[i]) << (uint(i) * 8)
	}
	return math.Float32frombits(bits)
}
