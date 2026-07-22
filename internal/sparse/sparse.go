// Package sparse is a faithful pure-Go port of OpenJPEG's sparse_array.c
// (opj_sparse_array_int32). A sparse array stores a 2-D grid of int32 values
// as a set of fixed-size blocks; blocks that are never written stay nil and
// read back as zero. It is used by the region/partial DWT decode paths.
//
// Semantics mirror the C code exactly, including every bounds check and the
// "forgiving" (best-effort) vs strict read/write behaviour. Callers pass the
// destination/source as a slice; where the C code offsets the pointer
// (e.g. dest + off) the Go caller passes an already-sliced buffer.
package sparse

// Array is a port of struct opj_sparse_array_int32.
type Array struct {
	width         uint32
	height        uint32
	blockWidth    uint32
	blockHeight   uint32
	blockCountHor uint32
	blockCountVer uint32
	dataBlocks    [][]int32 // nil entries denote unallocated (all-zero) blocks
}

// ceildiv is a port of opj_uint_ceildiv.
func ceildiv(a, b uint32) uint32 {
	return uint32((uint64(a) + uint64(b) - 1) / uint64(b))
}

func minU32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// New is a port of opj_sparse_array_int32_create. It returns nil (matching the
// C NULL return) when the dimensions are zero or would overflow.
func New(width, height, blockWidth, blockHeight uint32) *Array {
	if width == 0 || height == 0 || blockWidth == 0 || blockHeight == 0 {
		return nil
	}
	// block_width > (~0U) / block_height / sizeof(OPJ_INT32)
	if blockWidth > (^uint32(0))/blockHeight/4 {
		return nil
	}
	sa := &Array{
		width:         width,
		height:        height,
		blockWidth:    blockWidth,
		blockHeight:   blockHeight,
		blockCountHor: ceildiv(width, blockWidth),
		blockCountVer: ceildiv(height, blockHeight),
	}
	if sa.blockCountHor > (^uint32(0))/sa.blockCountVer {
		return nil
	}
	sa.dataBlocks = make([][]int32, uint64(sa.blockCountHor)*uint64(sa.blockCountVer))
	return sa
}

// IsRegionValid is a port of opj_sparse_array_is_region_valid.
func (sa *Array) IsRegionValid(x0, y0, x1, y1 uint32) bool {
	return !(x0 >= sa.width || x1 <= x0 || x1 > sa.width ||
		y0 >= sa.height || y1 <= y0 || y1 > sa.height)
}

// readOrWrite is a port of opj_sparse_array_int32_read_or_write.
//
// buf is the destination (read) or source (write) buffer, indexed relative to
// its element 0, exactly as the C code indexes buf. colStride/lineStride are in
// units of int32 elements.
func (sa *Array) readOrWrite(x0, y0, x1, y1 uint32, buf []int32,
	colStride, lineStride uint32, forgiving, isRead bool) bool {

	blockWidth := sa.blockWidth

	if !sa.IsRegionValid(x0, y0, x1, y1) {
		return forgiving
	}

	blockY := y0 / sa.blockHeight
	for y := y0; y < y1; blockY++ {
		var yIncr uint32
		if y == y0 {
			yIncr = sa.blockHeight - (y0 % sa.blockHeight)
		} else {
			yIncr = sa.blockHeight
		}
		blockYOffset := sa.blockHeight - yIncr
		yIncr = minU32(yIncr, y1-y)

		blockX := x0 / blockWidth
		for x := x0; x < x1; blockX++ {
			var xIncr uint32
			if x == x0 {
				xIncr = blockWidth - (x0 % blockWidth)
			} else {
				xIncr = blockWidth
			}
			blockXOffset := blockWidth - xIncr
			xIncr = minU32(xIncr, x1-x)

			srcBlock := sa.dataBlocks[blockY*sa.blockCountHor+blockX]

			bufBase := int(y-y0)*int(lineStride) + int(x-x0)*int(colStride)

			if isRead {
				if srcBlock == nil {
					for j := uint32(0); j < yIncr; j++ {
						row := bufBase + int(j)*int(lineStride)
						for k := uint32(0); k < xIncr; k++ {
							buf[row+int(k)*int(colStride)] = 0
						}
					}
				} else {
					srcBase := int(blockYOffset)*int(blockWidth) + int(blockXOffset)
					for j := uint32(0); j < yIncr; j++ {
						drow := bufBase + int(j)*int(lineStride)
						srow := srcBase + int(j)*int(blockWidth)
						for k := uint32(0); k < xIncr; k++ {
							buf[drow+int(k)*int(colStride)] = srcBlock[srow+int(k)]
						}
					}
				}
			} else {
				if srcBlock == nil {
					srcBlock = make([]int32, int(sa.blockWidth)*int(sa.blockHeight))
					sa.dataBlocks[blockY*sa.blockCountHor+blockX] = srcBlock
				}
				dstBase := int(blockYOffset)*int(blockWidth) + int(blockXOffset)
				for j := uint32(0); j < yIncr; j++ {
					drow := dstBase + int(j)*int(blockWidth)
					srow := bufBase + int(j)*int(lineStride)
					for k := uint32(0); k < xIncr; k++ {
						srcBlock[drow+int(k)] = buf[srow+int(k)*int(colStride)]
					}
				}
			}

			x += xIncr
		}

		y += yIncr
	}

	return true
}

// Read is a port of opj_sparse_array_int32_read. destColStride and
// destLineStride are in int32 elements; dest is indexed from its element 0.
func (sa *Array) Read(x0, y0, x1, y1 uint32, dest []int32,
	destColStride, destLineStride uint32, forgiving bool) bool {
	return sa.readOrWrite(x0, y0, x1, y1, dest, destColStride, destLineStride,
		forgiving, true)
}

// Write is a port of opj_sparse_array_int32_write. srcColStride and
// srcLineStride are in int32 elements; src is indexed from its element 0.
func (sa *Array) Write(x0, y0, x1, y1 uint32, src []int32,
	srcColStride, srcLineStride uint32, forgiving bool) bool {
	return sa.readOrWrite(x0, y0, x1, y1, src, srcColStride, srcLineStride,
		forgiving, false)
}
