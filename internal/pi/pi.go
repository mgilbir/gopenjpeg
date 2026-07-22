// Package pi ports pi.c/pi.h: the packet iterator that yields, for a tile, the
// sequence of (component, resolution, precinct, layer) packets in the order
// dictated by the progression order(s) and any POC (progression-order-change)
// markers. It is used by the tier-2 (t2) code.
//
// The C code resumes the nested progression loops with a `goto LABEL_SKIP` into
// the innermost loop. Go's goto cannot jump into a loop, so this port uses a
// `skip` flag that reproduces the same resume behaviour: on a non-first call the
// saved loop variables are kept and the innermost loop advances one position
// before continuing normally. Deterministic recomputations along the resume
// descent (precinct geometry) yield the same values they had when the packet was
// first produced, so they are allowed to run unguarded.
package pi

import (
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
)

const uintMax = 0xffffffff

// Resolution ports opj_pi_resolution_t.
type Resolution struct {
	Pdx uint32 // pdx
	Pdy uint32 // pdy
	Pw  uint32 // pw
	Ph  uint32 // ph
}

// Comp ports opj_pi_comp_t.
type Comp struct {
	Dx             uint32       // dx
	Dy             uint32       // dy
	Numresolutions uint32       // numresolutions
	Resolutions    []Resolution // resolutions
}

// Iterator ports opj_pi_iterator_t.
type Iterator struct {
	tpOn byte // tp_on: enabling tile-part generation

	// include and includeSize port pi->include / pi->include_size. The include
	// slice is shared by all iterators created together (one per POC), matching
	// the C code where later iterators alias the first iterator's include array.
	include     []int16
	includeSize uint32

	stepL uint32 // step_l
	stepR uint32 // step_r
	stepC uint32 // step_c
	stepP uint32 // step_p

	compno uint32 // compno
	resno  uint32 // resno
	precno uint32 // precno
	layno  uint32 // layno

	first bool // first: true before the first packet is produced

	poc cparams.POC // poc: progression-order-change info

	numcomps uint32 // numcomps
	comps    []Comp // comps

	tx0 uint32 // tx0
	ty0 uint32 // ty0
	tx1 uint32 // tx1
	ty1 uint32 // ty1

	x  uint32 // x
	y  uint32 // y
	dx uint32 // dx
	dy uint32 // dy

	manager *event.Manager // event manager
}

// Compno returns pi->compno (the component of the current packet).
func (pi *Iterator) Compno() uint32 { return pi.compno }

// Resno returns pi->resno.
func (pi *Iterator) Resno() uint32 { return pi.resno }

// Precno returns pi->precno.
func (pi *Iterator) Precno() uint32 { return pi.precno }

// Layno returns pi->layno.
func (pi *Iterator) Layno() uint32 { return pi.layno }

// Prg returns pi->poc.prg (used by t2 to detect OPJ_PROG_UNKNOWN).
func (pi *Iterator) Prg() cparams.ProgOrder { return pi.poc.Prg }

func (pi *Iterator) errorf(format string, args ...any) {
	if pi.manager != nil {
		pi.manager.Errorf(format, args...)
	}
}

/*
==========================================================
   local functions
==========================================================
*/

// nextLRCP ports opj_pi_next_lrcp.
func (pi *Iterator) nextLRCP() bool {
	if pi.poc.Compno0 >= pi.numcomps || pi.poc.Compno1 >= pi.numcomps+1 {
		pi.errorf("opj_pi_next_lrcp(): invalid compno0/compno1\n")
		return false
	}

	skip := !pi.first
	if pi.first {
		pi.first = false
		pi.layno = pi.poc.Layno0
	}

	for ; pi.layno < pi.poc.Layno1; pi.layno++ {
		if !skip {
			pi.resno = pi.poc.Resno0
		}
		for ; pi.resno < pi.poc.Resno1; pi.resno++ {
			if !skip {
				pi.compno = pi.poc.Compno0
			}
			for ; pi.compno < pi.poc.Compno1; pi.compno++ {
				comp := &pi.comps[pi.compno]
				if pi.resno >= comp.Numresolutions {
					continue
				}
				res := &comp.Resolutions[pi.resno]
				if pi.tpOn == 0 {
					pi.poc.Precno1 = res.Pw * res.Ph
				}
				if !skip {
					pi.precno = pi.poc.Precno0
				}
				for ; pi.precno < pi.poc.Precno1; pi.precno++ {
					if skip {
						skip = false
						continue
					}
					index := pi.layno*pi.stepL + pi.resno*pi.stepR + pi.compno*pi.stepC + pi.precno*pi.stepP
					if index >= pi.includeSize {
						pi.errorf("Invalid access to pi->include")
						return false
					}
					if pi.include[index] == 0 {
						pi.include[index] = 1
						return true
					}
				}
			}
		}
	}
	return false
}

// nextRLCP ports opj_pi_next_rlcp.
func (pi *Iterator) nextRLCP() bool {
	if pi.poc.Compno0 >= pi.numcomps || pi.poc.Compno1 >= pi.numcomps+1 {
		pi.errorf("opj_pi_next_rlcp(): invalid compno0/compno1\n")
		return false
	}

	skip := !pi.first
	if pi.first {
		pi.first = false
		pi.resno = pi.poc.Resno0
	}

	for ; pi.resno < pi.poc.Resno1; pi.resno++ {
		if !skip {
			pi.layno = pi.poc.Layno0
		}
		for ; pi.layno < pi.poc.Layno1; pi.layno++ {
			if !skip {
				pi.compno = pi.poc.Compno0
			}
			for ; pi.compno < pi.poc.Compno1; pi.compno++ {
				comp := &pi.comps[pi.compno]
				if pi.resno >= comp.Numresolutions {
					continue
				}
				res := &comp.Resolutions[pi.resno]
				if pi.tpOn == 0 {
					pi.poc.Precno1 = res.Pw * res.Ph
				}
				if !skip {
					pi.precno = pi.poc.Precno0
				}
				for ; pi.precno < pi.poc.Precno1; pi.precno++ {
					if skip {
						skip = false
						continue
					}
					index := pi.layno*pi.stepL + pi.resno*pi.stepR + pi.compno*pi.stepC + pi.precno*pi.stepP
					if index >= pi.includeSize {
						pi.errorf("Invalid access to pi->include")
						return false
					}
					if pi.include[index] == 0 {
						pi.include[index] = 1
						return true
					}
				}
			}
		}
	}
	return false
}

// computeSpatialStep ports the dx/dy computation shared by rpcl/pcrl/cprl for
// the whole image (compno loop over all components).
func (pi *Iterator) computeSpatialStep() {
	pi.dx = 0
	pi.dy = 0
	for compno := uint32(0); compno < pi.numcomps; compno++ {
		comp := &pi.comps[compno]
		for resno := uint32(0); resno < comp.Numresolutions; resno++ {
			res := &comp.Resolutions[resno]
			shx := res.Pdx + comp.Numresolutions - 1 - resno
			if shx < 32 && comp.Dx <= uintMax/(uint32(1)<<shx) {
				dx := comp.Dx * (uint32(1) << shx)
				if pi.dx == 0 {
					pi.dx = dx
				} else {
					pi.dx = opjmath.UintMin(pi.dx, dx)
				}
			}
			shy := res.Pdy + comp.Numresolutions - 1 - resno
			if shy < 32 && comp.Dy <= uintMax/(uint32(1)<<shy) {
				dy := comp.Dy * (uint32(1) << shy)
				if pi.dy == 0 {
					pi.dy = dy
				} else {
					pi.dy = opjmath.UintMin(pi.dy, dy)
				}
			}
		}
	}
}

// nextRPCL ports opj_pi_next_rpcl.
func (pi *Iterator) nextRPCL() bool {
	if pi.poc.Compno0 >= pi.numcomps || pi.poc.Compno1 >= pi.numcomps+1 {
		pi.errorf("opj_pi_next_rpcl(): invalid compno0/compno1\n")
		return false
	}

	skip := !pi.first
	if pi.first {
		pi.first = false
		pi.computeSpatialStep()
		if pi.dx == 0 || pi.dy == 0 {
			return false
		}
	}
	if !skip {
		if pi.tpOn == 0 {
			pi.poc.Ty0 = pi.ty0
			pi.poc.Tx0 = pi.tx0
			pi.poc.Ty1 = pi.ty1
			pi.poc.Tx1 = pi.tx1
		}
		pi.resno = pi.poc.Resno0
	}

	for ; pi.resno < pi.poc.Resno1; pi.resno++ {
		if !skip {
			pi.y = pi.poc.Ty0
		}
		for ; pi.y < pi.poc.Ty1; pi.y += pi.dy - (pi.y % pi.dy) {
			if !skip {
				pi.x = pi.poc.Tx0
			}
			for ; pi.x < pi.poc.Tx1; pi.x += pi.dx - (pi.x % pi.dx) {
				if !skip {
					pi.compno = pi.poc.Compno0
				}
				for ; pi.compno < pi.poc.Compno1; pi.compno++ {
					comp := &pi.comps[pi.compno]
					if pi.resno >= comp.Numresolutions {
						continue
					}
					res := &comp.Resolutions[pi.resno]
					levelno := comp.Numresolutions - 1 - pi.resno

					if uint32((uint64(comp.Dx)<<levelno)>>levelno) != comp.Dx ||
						uint32((uint64(comp.Dy)<<levelno)>>levelno) != comp.Dy {
						continue
					}

					trx0 := opjmath.Uint64CeildivResUint32(uint64(pi.tx0), uint64(comp.Dx)<<levelno)
					try0 := opjmath.Uint64CeildivResUint32(uint64(pi.ty0), uint64(comp.Dy)<<levelno)
					trx1 := opjmath.Uint64CeildivResUint32(uint64(pi.tx1), uint64(comp.Dx)<<levelno)
					try1 := opjmath.Uint64CeildivResUint32(uint64(pi.ty1), uint64(comp.Dy)<<levelno)
					rpx := res.Pdx + levelno
					rpy := res.Pdy + levelno

					if uint32((uint64(comp.Dx)<<rpx)>>rpx) != comp.Dx ||
						uint32((uint64(comp.Dy)<<rpy)>>rpy) != comp.Dy {
						continue
					}

					if !((uint64(pi.y)%(uint64(comp.Dy)<<rpy) == 0) ||
						(pi.y == pi.ty0 && ((uint64(try0)<<levelno)%(uint64(1)<<rpy)) != 0)) {
						continue
					}
					if !((uint64(pi.x)%(uint64(comp.Dx)<<rpx) == 0) ||
						(pi.x == pi.tx0 && ((uint64(trx0)<<levelno)%(uint64(1)<<rpx)) != 0)) {
						continue
					}

					if res.Pw == 0 || res.Ph == 0 {
						continue
					}
					if trx0 == trx1 || try0 == try1 {
						continue
					}

					prci := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.x), uint64(comp.Dx)<<levelno), res.Pdx) -
						opjmath.UintFloordivpow2(trx0, res.Pdx)
					prcj := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.y), uint64(comp.Dy)<<levelno), res.Pdy) -
						opjmath.UintFloordivpow2(try0, res.Pdy)
					pi.precno = prci + prcj*res.Pw
					if !skip {
						pi.layno = pi.poc.Layno0
					}
					for ; pi.layno < pi.poc.Layno1; pi.layno++ {
						if skip {
							skip = false
							continue
						}
						index := pi.layno*pi.stepL + pi.resno*pi.stepR + pi.compno*pi.stepC + pi.precno*pi.stepP
						if index >= pi.includeSize {
							pi.errorf("Invalid access to pi->include")
							return false
						}
						if pi.include[index] == 0 {
							pi.include[index] = 1
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// nextPCRL ports opj_pi_next_pcrl.
func (pi *Iterator) nextPCRL() bool {
	if pi.poc.Compno0 >= pi.numcomps || pi.poc.Compno1 >= pi.numcomps+1 {
		pi.errorf("opj_pi_next_pcrl(): invalid compno0/compno1\n")
		return false
	}

	skip := !pi.first
	if pi.first {
		pi.first = false
		pi.computeSpatialStep()
		if pi.dx == 0 || pi.dy == 0 {
			return false
		}
	}
	if !skip {
		if pi.tpOn == 0 {
			pi.poc.Ty0 = pi.ty0
			pi.poc.Tx0 = pi.tx0
			pi.poc.Ty1 = pi.ty1
			pi.poc.Tx1 = pi.tx1
		}
		pi.y = pi.poc.Ty0
	}

	for ; pi.y < pi.poc.Ty1; pi.y += pi.dy - (pi.y % pi.dy) {
		if !skip {
			pi.x = pi.poc.Tx0
		}
		for ; pi.x < pi.poc.Tx1; pi.x += pi.dx - (pi.x % pi.dx) {
			if !skip {
				pi.compno = pi.poc.Compno0
			}
			for ; pi.compno < pi.poc.Compno1; pi.compno++ {
				comp := &pi.comps[pi.compno]
				if !skip {
					pi.resno = pi.poc.Resno0
				}
				for ; pi.resno < opjmath.UintMin(pi.poc.Resno1, comp.Numresolutions); pi.resno++ {
					res := &comp.Resolutions[pi.resno]
					levelno := comp.Numresolutions - 1 - pi.resno

					if uint32((uint64(comp.Dx)<<levelno)>>levelno) != comp.Dx ||
						uint32((uint64(comp.Dy)<<levelno)>>levelno) != comp.Dy {
						continue
					}

					trx0 := opjmath.Uint64CeildivResUint32(uint64(pi.tx0), uint64(comp.Dx)<<levelno)
					try0 := opjmath.Uint64CeildivResUint32(uint64(pi.ty0), uint64(comp.Dy)<<levelno)
					trx1 := opjmath.Uint64CeildivResUint32(uint64(pi.tx1), uint64(comp.Dx)<<levelno)
					try1 := opjmath.Uint64CeildivResUint32(uint64(pi.ty1), uint64(comp.Dy)<<levelno)
					rpx := res.Pdx + levelno
					rpy := res.Pdy + levelno

					if uint32((uint64(comp.Dx)<<rpx)>>rpx) != comp.Dx ||
						uint32((uint64(comp.Dy)<<rpy)>>rpy) != comp.Dy {
						continue
					}

					if !((uint64(pi.y)%(uint64(comp.Dy)<<rpy) == 0) ||
						(pi.y == pi.ty0 && ((uint64(try0)<<levelno)%(uint64(1)<<rpy)) != 0)) {
						continue
					}
					if !((uint64(pi.x)%(uint64(comp.Dx)<<rpx) == 0) ||
						(pi.x == pi.tx0 && ((uint64(trx0)<<levelno)%(uint64(1)<<rpx)) != 0)) {
						continue
					}

					if res.Pw == 0 || res.Ph == 0 {
						continue
					}
					if trx0 == trx1 || try0 == try1 {
						continue
					}

					prci := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.x), uint64(comp.Dx)<<levelno), res.Pdx) -
						opjmath.UintFloordivpow2(trx0, res.Pdx)
					prcj := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.y), uint64(comp.Dy)<<levelno), res.Pdy) -
						opjmath.UintFloordivpow2(try0, res.Pdy)
					pi.precno = prci + prcj*res.Pw
					if !skip {
						pi.layno = pi.poc.Layno0
					}
					for ; pi.layno < pi.poc.Layno1; pi.layno++ {
						if skip {
							skip = false
							continue
						}
						index := pi.layno*pi.stepL + pi.resno*pi.stepR + pi.compno*pi.stepC + pi.precno*pi.stepP
						if index >= pi.includeSize {
							pi.errorf("Invalid access to pi->include")
							return false
						}
						if pi.include[index] == 0 {
							pi.include[index] = 1
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// nextCPRL ports opj_pi_next_cprl.
func (pi *Iterator) nextCPRL() bool {
	if pi.poc.Compno0 >= pi.numcomps || pi.poc.Compno1 >= pi.numcomps+1 {
		pi.errorf("opj_pi_next_cprl(): invalid compno0/compno1\n")
		return false
	}

	skip := !pi.first
	if pi.first {
		pi.first = false
		pi.compno = pi.poc.Compno0
	}

	for ; pi.compno < pi.poc.Compno1; pi.compno++ {
		comp := &pi.comps[pi.compno]
		if !skip {
			// dx/dy for this component only (C recomputes per component).
			pi.dx = 0
			pi.dy = 0
			for resno := uint32(0); resno < comp.Numresolutions; resno++ {
				res := &comp.Resolutions[resno]
				shx := res.Pdx + comp.Numresolutions - 1 - resno
				if shx < 32 && comp.Dx <= uintMax/(uint32(1)<<shx) {
					dx := comp.Dx * (uint32(1) << shx)
					if pi.dx == 0 {
						pi.dx = dx
					} else {
						pi.dx = opjmath.UintMin(pi.dx, dx)
					}
				}
				shy := res.Pdy + comp.Numresolutions - 1 - resno
				if shy < 32 && comp.Dy <= uintMax/(uint32(1)<<shy) {
					dy := comp.Dy * (uint32(1) << shy)
					if pi.dy == 0 {
						pi.dy = dy
					} else {
						pi.dy = opjmath.UintMin(pi.dy, dy)
					}
				}
			}
			if pi.dx == 0 || pi.dy == 0 {
				return false
			}
			if pi.tpOn == 0 {
				pi.poc.Ty0 = pi.ty0
				pi.poc.Tx0 = pi.tx0
				pi.poc.Ty1 = pi.ty1
				pi.poc.Tx1 = pi.tx1
			}
			pi.y = pi.poc.Ty0
		}
		for ; pi.y < pi.poc.Ty1; pi.y += pi.dy - (pi.y % pi.dy) {
			if !skip {
				pi.x = pi.poc.Tx0
			}
			for ; pi.x < pi.poc.Tx1; pi.x += pi.dx - (pi.x % pi.dx) {
				if !skip {
					pi.resno = pi.poc.Resno0
				}
				for ; pi.resno < opjmath.UintMin(pi.poc.Resno1, comp.Numresolutions); pi.resno++ {
					res := &comp.Resolutions[pi.resno]
					levelno := comp.Numresolutions - 1 - pi.resno

					if uint32((uint64(comp.Dx)<<levelno)>>levelno) != comp.Dx ||
						uint32((uint64(comp.Dy)<<levelno)>>levelno) != comp.Dy {
						continue
					}

					trx0 := opjmath.Uint64CeildivResUint32(uint64(pi.tx0), uint64(comp.Dx)<<levelno)
					try0 := opjmath.Uint64CeildivResUint32(uint64(pi.ty0), uint64(comp.Dy)<<levelno)
					trx1 := opjmath.Uint64CeildivResUint32(uint64(pi.tx1), uint64(comp.Dx)<<levelno)
					try1 := opjmath.Uint64CeildivResUint32(uint64(pi.ty1), uint64(comp.Dy)<<levelno)
					rpx := res.Pdx + levelno
					rpy := res.Pdy + levelno

					if uint32((uint64(comp.Dx)<<rpx)>>rpx) != comp.Dx ||
						uint32((uint64(comp.Dy)<<rpy)>>rpy) != comp.Dy {
						continue
					}

					if !((uint64(pi.y)%(uint64(comp.Dy)<<rpy) == 0) ||
						(pi.y == pi.ty0 && ((uint64(try0)<<levelno)%(uint64(1)<<rpy)) != 0)) {
						continue
					}
					if !((uint64(pi.x)%(uint64(comp.Dx)<<rpx) == 0) ||
						(pi.x == pi.tx0 && ((uint64(trx0)<<levelno)%(uint64(1)<<rpx)) != 0)) {
						continue
					}

					if res.Pw == 0 || res.Ph == 0 {
						continue
					}
					if trx0 == trx1 || try0 == try1 {
						continue
					}

					prci := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.x), uint64(comp.Dx)<<levelno), res.Pdx) -
						opjmath.UintFloordivpow2(trx0, res.Pdx)
					prcj := opjmath.UintFloordivpow2(opjmath.Uint64CeildivResUint32(uint64(pi.y), uint64(comp.Dy)<<levelno), res.Pdy) -
						opjmath.UintFloordivpow2(try0, res.Pdy)
					pi.precno = prci + prcj*res.Pw
					if !skip {
						pi.layno = pi.poc.Layno0
					}
					for ; pi.layno < pi.poc.Layno1; pi.layno++ {
						if skip {
							skip = false
							continue
						}
						index := pi.layno*pi.stepL + pi.resno*pi.stepR + pi.compno*pi.stepC + pi.precno*pi.stepP
						if index >= pi.includeSize {
							pi.errorf("Invalid access to pi->include")
							return false
						}
						if pi.include[index] == 0 {
							pi.include[index] = 1
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// Next ports opj_pi_next: advance to the next packet. It returns false when the
// iterator has produced its last packet.
func (pi *Iterator) Next() bool {
	switch pi.poc.Prg {
	case cparams.LRCP:
		return pi.nextLRCP()
	case cparams.RLCP:
		return pi.nextRLCP()
	case cparams.RPCL:
		return pi.nextRPCL()
	case cparams.PCRL:
		return pi.nextPCRL()
	case cparams.CPRL:
		return pi.nextCPRL()
	case cparams.ProgUnknown:
		return false
	}
	return false
}

// getEncodingParameters ports opj_get_encoding_parameters: compute the tile
// extent, minimum dx/dy, maximum precinct count and maximum resolution count.
func getEncodingParameters(img *image.Image, cp *cparams.CP, tileno uint32) (tx0, tx1, ty0, ty1, dxMin, dyMin, maxPrec, maxRes uint32) {
	tcp := &cp.Tcps[tileno]

	p := tileno % cp.Tw
	q := tileno / cp.Tw

	ltx0 := cp.Tx0 + p*cp.Tdx
	tx0 = opjmath.UintMax(ltx0, img.X0)
	tx1 = opjmath.UintMin(opjmath.UintAdds(ltx0, cp.Tdx), img.X1)
	lty0 := cp.Ty0 + q*cp.Tdy
	ty0 = opjmath.UintMax(lty0, img.Y0)
	ty1 = opjmath.UintMin(opjmath.UintAdds(lty0, cp.Tdy), img.Y1)

	maxPrec = 0
	maxRes = 0
	dxMin = 0x7fffffff
	dyMin = 0x7fffffff

	for compno := uint32(0); compno < img.Numcomps; compno++ {
		imgComp := &img.Comps[compno]
		tccp := &tcp.TCCPs[compno]

		tcx0 := opjmath.UintCeildiv(tx0, imgComp.Dx)
		tcy0 := opjmath.UintCeildiv(ty0, imgComp.Dy)
		tcx1 := opjmath.UintCeildiv(tx1, imgComp.Dx)
		tcy1 := opjmath.UintCeildiv(ty1, imgComp.Dy)

		if tccp.Numresolutions > maxRes {
			maxRes = tccp.Numresolutions
		}

		for resno := uint32(0); resno < tccp.Numresolutions; resno++ {
			pdx := tccp.Prcw[resno]
			pdy := tccp.Prch[resno]

			ldx := uint64(imgComp.Dx) * (uint64(1) << (pdx + tccp.Numresolutions - 1 - resno))
			ldy := uint64(imgComp.Dy) * (uint64(1) << (pdy + tccp.Numresolutions - 1 - resno))

			if ldx <= uintMax {
				dxMin = opjmath.UintMin(dxMin, uint32(ldx))
			}
			if ldy <= uintMax {
				dyMin = opjmath.UintMin(dyMin, uint32(ldy))
			}

			levelNo := tccp.Numresolutions - 1 - resno

			rx0 := opjmath.UintCeildivpow2(tcx0, levelNo)
			ry0 := opjmath.UintCeildivpow2(tcy0, levelNo)
			rx1 := opjmath.UintCeildivpow2(tcx1, levelNo)
			ry1 := opjmath.UintCeildivpow2(tcy1, levelNo)

			px0 := opjmath.UintFloordivpow2(rx0, pdx) << pdx
			py0 := opjmath.UintFloordivpow2(ry0, pdy) << pdy
			px1 := opjmath.UintCeildivpow2(rx1, pdx) << pdx
			py1 := opjmath.UintCeildivpow2(ry1, pdy) << pdy

			var pw, ph uint32
			if rx0 != rx1 {
				pw = (px1 - px0) >> pdx
			}
			if ry0 != ry1 {
				ph = (py1 - py0) >> pdy
			}

			product := pw * ph
			if product > maxPrec {
				maxPrec = product
			}
		}
	}
	return
}

// getAllEncodingParameters ports opj_get_all_encoding_parameters. In addition to
// the values returned by getEncodingParameters, it fills, for each component,
// resolutions[compno] with the tuple (pdx, pdy, pw, ph) per resolution (when the
// slice is non-nil).
func getAllEncodingParameters(img *image.Image, cp *cparams.CP, tileno uint32, resolutions [][]uint32) (tx0, tx1, ty0, ty1, dxMin, dyMin, maxPrec, maxRes uint32) {
	tcp := &cp.Tcps[tileno]

	p := tileno % cp.Tw
	q := tileno / cp.Tw

	ltx0 := cp.Tx0 + p*cp.Tdx
	tx0 = opjmath.UintMax(ltx0, img.X0)
	tx1 = opjmath.UintMin(opjmath.UintAdds(ltx0, cp.Tdx), img.X1)
	lty0 := cp.Ty0 + q*cp.Tdy
	ty0 = opjmath.UintMax(lty0, img.Y0)
	ty1 = opjmath.UintMin(opjmath.UintAdds(lty0, cp.Tdy), img.Y1)

	maxPrec = 0
	maxRes = 0
	dxMin = 0x7fffffff
	dyMin = 0x7fffffff

	for compno := uint32(0); compno < img.Numcomps; compno++ {
		imgComp := &img.Comps[compno]
		tccp := &tcp.TCCPs[compno]

		var resPtr []uint32
		if resolutions != nil {
			resPtr = resolutions[compno]
		}
		w := 0

		tcx0 := opjmath.UintCeildiv(tx0, imgComp.Dx)
		tcy0 := opjmath.UintCeildiv(ty0, imgComp.Dy)
		tcx1 := opjmath.UintCeildiv(tx1, imgComp.Dx)
		tcy1 := opjmath.UintCeildiv(ty1, imgComp.Dy)

		if tccp.Numresolutions > maxRes {
			maxRes = tccp.Numresolutions
		}

		levelNo := tccp.Numresolutions
		for resno := uint32(0); resno < tccp.Numresolutions; resno++ {
			levelNo--

			pdx := tccp.Prcw[resno]
			pdy := tccp.Prch[resno]
			if resPtr != nil {
				resPtr[w] = pdx
				w++
				resPtr[w] = pdy
				w++
			}
			if pdx+levelNo < 32 && imgComp.Dx <= uintMax/(uint32(1)<<(pdx+levelNo)) {
				ldx := imgComp.Dx * (uint32(1) << (pdx + levelNo))
				dxMin = opjmath.UintMin(dxMin, ldx)
			}
			if pdy+levelNo < 32 && imgComp.Dy <= uintMax/(uint32(1)<<(pdy+levelNo)) {
				ldy := imgComp.Dy * (uint32(1) << (pdy + levelNo))
				dyMin = opjmath.UintMin(dyMin, ldy)
			}

			rx0 := opjmath.UintCeildivpow2(tcx0, levelNo)
			ry0 := opjmath.UintCeildivpow2(tcy0, levelNo)
			rx1 := opjmath.UintCeildivpow2(tcx1, levelNo)
			ry1 := opjmath.UintCeildivpow2(tcy1, levelNo)
			px0 := opjmath.UintFloordivpow2(rx0, pdx) << pdx
			py0 := opjmath.UintFloordivpow2(ry0, pdy) << pdy
			px1 := opjmath.UintCeildivpow2(rx1, pdx) << pdx
			py1 := opjmath.UintCeildivpow2(ry1, pdy) << pdy
			var pw, ph uint32
			if rx0 != rx1 {
				pw = (px1 - px0) >> pdx
			}
			if ry0 != ry1 {
				ph = (py1 - py0) >> pdy
			}
			if resPtr != nil {
				resPtr[w] = pw
				w++
				resPtr[w] = ph
				w++
			}
			product := pw * ph
			if product > maxPrec {
				maxPrec = product
			}
		}
	}
	return
}

// create ports opj_pi_create: allocate poc_bound iterators, each with a comps
// array sized to the image, each comp with a resolutions array sized to its
// tccp. No include array is allocated here.
func create(img *image.Image, cp *cparams.CP, tileno uint32, manager *event.Manager) []Iterator {
	tcp := &cp.Tcps[tileno]
	pocBound := tcp.Numpocs + 1

	pis := make([]Iterator, pocBound)
	for pino := uint32(0); pino < pocBound; pino++ {
		cur := &pis[pino]
		cur.manager = manager
		cur.comps = make([]Comp, img.Numcomps)
		cur.numcomps = img.Numcomps
		for compno := uint32(0); compno < img.Numcomps; compno++ {
			comp := &cur.comps[compno]
			tccp := &tcp.TCCPs[compno]
			comp.Resolutions = make([]Resolution, tccp.Numresolutions)
			comp.Numresolutions = tccp.Numresolutions
		}
	}
	return pis
}

// updateEncodePocAndFinal ports opj_pi_update_encode_poc_and_final.
func updateEncodePocAndFinal(cp *cparams.CP, tileno uint32, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin uint32) {
	_ = maxRes // OPJ_ARG_NOT_USED(p_max_res)
	tcp := &cp.Tcps[tileno]
	pocBound := tcp.Numpocs + 1

	cur := &tcp.Pocs[0]
	cur.CompS = cur.Compno0
	cur.CompE = cur.Compno1
	cur.ResS = cur.Resno0
	cur.ResE = cur.Resno1
	cur.LayE = cur.Layno1

	cur.LayS = 0
	cur.Prg = cur.Prg1
	cur.PrcS = 0

	cur.PrcE = maxPrec
	cur.TxS = tx0
	cur.TxE = tx1
	cur.TyS = ty0
	cur.TyE = ty1
	cur.Dx = dxMin
	cur.Dy = dyMin

	for pino := uint32(1); pino < pocBound; pino++ {
		cur = &tcp.Pocs[pino]
		prev := &tcp.Pocs[pino-1]
		cur.CompS = cur.Compno0
		cur.CompE = cur.Compno1
		cur.ResS = cur.Resno0
		cur.ResE = cur.Resno1
		cur.LayE = cur.Layno1
		cur.Prg = cur.Prg1
		cur.PrcS = 0
		if cur.LayE > prev.LayE {
			cur.LayS = cur.LayE
		} else {
			cur.LayS = 0
		}
		cur.PrcE = maxPrec
		cur.TxS = tx0
		cur.TxE = tx1
		cur.TyS = ty0
		cur.TyE = ty1
		cur.Dx = dxMin
		cur.Dy = dyMin
	}
}

// updateEncodeNotPoc ports opj_pi_update_encode_not_poc.
func updateEncodeNotPoc(cp *cparams.CP, numComps, tileno, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin uint32) {
	tcp := &cp.Tcps[tileno]
	pocBound := tcp.Numpocs + 1
	for pino := uint32(0); pino < pocBound; pino++ {
		cur := &tcp.Pocs[pino]
		cur.CompS = 0
		cur.CompE = numComps
		cur.ResS = 0
		cur.ResE = maxRes
		cur.LayS = 0
		cur.LayE = tcp.Numlayers
		cur.Prg = tcp.Prg
		cur.PrcS = 0
		cur.PrcE = maxPrec
		cur.TxS = tx0
		cur.TxE = tx1
		cur.TyS = ty0
		cur.TyE = ty1
		cur.Dx = dxMin
		cur.Dy = dyMin
	}
}

// updateDecodePoc ports opj_pi_update_decode_poc.
func updateDecodePoc(pis []Iterator, tcp *cparams.TCP, maxPrecision, maxRes uint32) {
	_ = maxRes // OPJ_ARG_NOT_USED(p_max_res)
	bound := tcp.Numpocs + 1
	for pino := uint32(0); pino < bound; pino++ {
		cur := &pis[pino]
		poc := &tcp.Pocs[pino]
		cur.poc.Prg = poc.Prg
		cur.first = true
		cur.poc.Resno0 = poc.Resno0
		cur.poc.Compno0 = poc.Compno0
		cur.poc.Layno0 = 0
		cur.poc.Precno0 = 0
		cur.poc.Resno1 = poc.Resno1
		cur.poc.Compno1 = poc.Compno1
		cur.poc.Layno1 = opjmath.UintMin(poc.Layno1, tcp.Numlayers)
		cur.poc.Precno1 = maxPrecision
	}
}

// updateDecodeNotPoc ports opj_pi_update_decode_not_poc.
func updateDecodeNotPoc(pis []Iterator, tcp *cparams.TCP, maxPrecision, maxRes uint32) {
	bound := tcp.Numpocs + 1
	for pino := uint32(0); pino < bound; pino++ {
		cur := &pis[pino]
		cur.poc.Prg = tcp.Prg
		cur.first = true
		cur.poc.Resno0 = 0
		cur.poc.Compno0 = 0
		cur.poc.Layno0 = 0
		cur.poc.Precno0 = 0
		cur.poc.Resno1 = maxRes
		cur.poc.Compno1 = cur.numcomps
		cur.poc.Layno1 = tcp.Numlayers
		cur.poc.Precno1 = maxPrecision
	}
}

// checkNextLevel ports opj_pi_check_next_level.
func checkNextLevel(pos int32, cp *cparams.CP, tileno, pino uint32, prog string) bool {
	tcp := &cp.Tcps[tileno].Pocs[pino]
	if pos >= 0 {
		for i := pos; i >= 0; i-- {
			switch prog[i] {
			case 'R':
				if tcp.ResT == tcp.ResE {
					return checkNextLevel(pos-1, cp, tileno, pino, prog)
				}
				return true
			case 'C':
				if tcp.CompT == tcp.CompE {
					return checkNextLevel(pos-1, cp, tileno, pino, prog)
				}
				return true
			case 'L':
				if tcp.LayT == tcp.LayE {
					return checkNextLevel(pos-1, cp, tileno, pino, prog)
				}
				return true
			case 'P':
				switch tcp.Prg {
				case cparams.LRCP, cparams.RLCP:
					if tcp.PrcT == tcp.PrcE {
						return checkNextLevel(i-1, cp, tileno, pino, prog)
					}
					return true
				default:
					if tcp.Tx0T == tcp.TxE {
						if tcp.Ty0T == tcp.TyE {
							return checkNextLevel(i-1, cp, tileno, pino, prog)
						}
						return true
					}
					return true
				}
			}
		}
	}
	return false
}

/*
==========================================================
   Packet iterator interface
==========================================================
*/

// CreateDecode ports opj_pi_create_decode: build the packet iterator array for
// decoding tile tileno. It returns nil on the C NULL-return conditions (the
// include-array overflow guard).
func CreateDecode(img *image.Image, cp *cparams.CP, tileno uint32, manager *event.Manager) []Iterator {
	numcomps := img.Numcomps
	tcp := &cp.Tcps[tileno]
	bound := tcp.Numpocs + 1

	// per-component (pdx,pdy,pw,ph) x maxres storage
	dataStride := uint32(4 * cparams.MaxRLvls)
	tmpPtr := make([][]uint32, numcomps)
	for compno := uint32(0); compno < numcomps; compno++ {
		tmpPtr[compno] = make([]uint32, dataStride)
	}

	pis := create(img, cp, tileno, manager)

	tx0, tx1, ty0, ty1, _, _, maxPrec, maxRes := getAllEncodingParameters(img, cp, tileno, tmpPtr)

	stepP := uint32(1)
	stepC := maxPrec * stepP
	stepR := numcomps * stepC
	stepL := maxRes * stepR

	cur := &pis[0]

	// include allocation with integer-overflow guard.
	// 0 < numlayers < 65536 (c.f. opj_j2k_read_cod). Uses (numlayers+1).
	var include []int16
	var includeSize uint32
	if stepL <= uintMax/(tcp.Numlayers+1) {
		includeSize = (tcp.Numlayers + 1) * stepL
		include = make([]int16, includeSize)
	}
	if include == nil {
		return nil
	}

	cur.tx0 = tx0
	cur.ty0 = ty0
	cur.tx1 = tx1
	cur.ty1 = ty1
	cur.stepP = stepP
	cur.stepC = stepC
	cur.stepR = stepR
	cur.stepL = stepL
	cur.include = include
	cur.includeSize = includeSize

	fillComps(cur, img, tmpPtr)

	for pino := uint32(1); pino < bound; pino++ {
		cur = &pis[pino]
		cur.tx0 = tx0
		cur.ty0 = ty0
		cur.tx1 = tx1
		cur.ty1 = ty1
		cur.stepP = stepP
		cur.stepC = stepC
		cur.stepR = stepR
		cur.stepL = stepL
		fillComps(cur, img, tmpPtr)
		// special treatment: share include with the previous iterator.
		cur.include = include
		cur.includeSize = includeSize
	}

	if tcp.POC != 0 {
		updateDecodePoc(pis, tcp, maxPrec, maxRes)
	} else {
		updateDecodeNotPoc(pis, tcp, maxPrec, maxRes)
	}
	return pis
}

// fillComps copies the per-comp dx/dy and per-resolution (pdx,pdy,pw,ph) tuples
// computed by getAllEncodingParameters into a packet iterator's comps.
func fillComps(cur *Iterator, img *image.Image, tmpPtr [][]uint32) {
	for compno := uint32(0); compno < cur.numcomps; compno++ {
		comp := &cur.comps[compno]
		vals := tmpPtr[compno]
		comp.Dx = img.Comps[compno].Dx
		comp.Dy = img.Comps[compno].Dy
		w := 0
		for resno := uint32(0); resno < comp.Numresolutions; resno++ {
			res := &comp.Resolutions[resno]
			res.Pdx = vals[w]
			w++
			res.Pdy = vals[w]
			w++
			res.Pw = vals[w]
			w++
			res.Ph = vals[w]
			w++
		}
	}
}

// GetEncodingPacketCount ports opj_get_encoding_packet_count.
func GetEncodingPacketCount(img *image.Image, cp *cparams.CP, tileno uint32) uint32 {
	_, _, _, _, _, _, maxPrec, maxRes := getAllEncodingParameters(img, cp, tileno, nil)
	return cp.Tcps[tileno].Numlayers * maxPrec * img.Numcomps * maxRes
}

// InitialiseEncode ports opj_pi_initialise_encode: build the packet iterator
// array for encoding tile tileno, then update the tile's POCs. It returns nil on
// the include-array overflow guard.
func InitialiseEncode(img *image.Image, cp *cparams.CP, tileno uint32, t2Mode cparams.T2Mode, manager *event.Manager) []Iterator {
	numcomps := img.Numcomps
	tcp := &cp.Tcps[tileno]
	bound := tcp.Numpocs + 1

	dataStride := uint32(4 * cparams.MaxRLvls)
	tmpPtr := make([][]uint32, numcomps)
	for compno := uint32(0); compno < numcomps; compno++ {
		tmpPtr[compno] = make([]uint32, dataStride)
	}

	pis := create(img, cp, tileno, manager)

	tx0, tx1, ty0, ty1, dxMin, dyMin, maxPrec, maxRes := getAllEncodingParameters(img, cp, tileno, tmpPtr)

	stepP := uint32(1)
	stepC := maxPrec * stepP
	stepR := numcomps * stepC
	stepL := maxRes * stepR

	pis[0].tpOn = byte(cp.MEnc.MTpOn)
	cur := &pis[0]

	// include allocation with integer-overflow guard. Encode uses numlayers
	// (not numlayers+1).
	var include []int16
	var includeSize uint32
	if tcp.Numlayers != 0 && stepL <= uintMax/tcp.Numlayers {
		includeSize = tcp.Numlayers * stepL
		include = make([]int16, includeSize)
	}
	if include == nil {
		return nil
	}

	cur.tx0 = tx0
	cur.ty0 = ty0
	cur.tx1 = tx1
	cur.ty1 = ty1
	cur.dx = dxMin
	cur.dy = dyMin
	cur.stepP = stepP
	cur.stepC = stepC
	cur.stepR = stepR
	cur.stepL = stepL
	cur.include = include
	cur.includeSize = includeSize

	fillComps(cur, img, tmpPtr)

	for pino := uint32(1); pino < bound; pino++ {
		cur = &pis[pino]
		cur.tx0 = tx0
		cur.ty0 = ty0
		cur.tx1 = tx1
		cur.ty1 = ty1
		cur.dx = dxMin
		cur.dy = dyMin
		cur.stepP = stepP
		cur.stepC = stepC
		cur.stepR = stepR
		cur.stepL = stepL
		fillComps(cur, img, tmpPtr)
		cur.include = include
		cur.includeSize = includeSize
	}

	if tcp.POC != 0 && (cparams.IsCinema(cp.Rsiz) || t2Mode == cparams.FinalPass) {
		updateEncodePocAndFinal(cp, tileno, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin)
	} else {
		updateEncodeNotPoc(cp, numcomps, tileno, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin)
	}

	return pis
}

// CreateEncode ports opj_pi_create_encode: set up iterator pino for tile-part
// tpnum, honouring the tile-part flag position tppos and progression order.
func CreateEncode(pis []Iterator, cp *cparams.CP, tileno, pino, tpnum uint32, tppos int32, t2Mode cparams.T2Mode) {
	tcp := &cp.Tcps[tileno].Pocs[pino]
	prog := cparams.ConvertProgressionOrder(tcp.Prg)

	pis[pino].first = true
	pis[pino].poc.Prg = tcp.Prg

	tpOn := cp.MEnc.MTpOn != 0
	cinema := cparams.IsCinema(cp.Rsiz)
	imf := cparams.IsIMF(cp.Rsiz)

	if !(tpOn && ((!cinema && !imf && t2Mode == cparams.FinalPass) || cinema || imf)) {
		pis[pino].poc.Resno0 = tcp.ResS
		pis[pino].poc.Resno1 = tcp.ResE
		pis[pino].poc.Compno0 = tcp.CompS
		pis[pino].poc.Compno1 = tcp.CompE
		pis[pino].poc.Layno0 = tcp.LayS
		pis[pino].poc.Layno1 = tcp.LayE
		pis[pino].poc.Precno0 = tcp.PrcS
		pis[pino].poc.Precno1 = tcp.PrcE
		pis[pino].poc.Tx0 = tcp.TxS
		pis[pino].poc.Ty0 = tcp.TyS
		pis[pino].poc.Tx1 = tcp.TxE
		pis[pino].poc.Ty1 = tcp.TyE
		return
	}

	for i := tppos + 1; i < 4; i++ {
		switch prog[i] {
		case 'R':
			pis[pino].poc.Resno0 = tcp.ResS
			pis[pino].poc.Resno1 = tcp.ResE
		case 'C':
			pis[pino].poc.Compno0 = tcp.CompS
			pis[pino].poc.Compno1 = tcp.CompE
		case 'L':
			pis[pino].poc.Layno0 = tcp.LayS
			pis[pino].poc.Layno1 = tcp.LayE
		case 'P':
			switch tcp.Prg {
			case cparams.LRCP, cparams.RLCP:
				pis[pino].poc.Precno0 = tcp.PrcS
				pis[pino].poc.Precno1 = tcp.PrcE
			default:
				pis[pino].poc.Tx0 = tcp.TxS
				pis[pino].poc.Ty0 = tcp.TyS
				pis[pino].poc.Tx1 = tcp.TxE
				pis[pino].poc.Ty1 = tcp.TyE
			}
		}
	}

	if tpnum == 0 {
		for i := tppos; i >= 0; i-- {
			switch prog[i] {
			case 'C':
				tcp.CompT = tcp.CompS
				pis[pino].poc.Compno0 = tcp.CompT
				pis[pino].poc.Compno1 = tcp.CompT + 1
				tcp.CompT++
			case 'R':
				tcp.ResT = tcp.ResS
				pis[pino].poc.Resno0 = tcp.ResT
				pis[pino].poc.Resno1 = tcp.ResT + 1
				tcp.ResT++
			case 'L':
				tcp.LayT = tcp.LayS
				pis[pino].poc.Layno0 = tcp.LayT
				pis[pino].poc.Layno1 = tcp.LayT + 1
				tcp.LayT++
			case 'P':
				switch tcp.Prg {
				case cparams.LRCP, cparams.RLCP:
					tcp.PrcT = tcp.PrcS
					pis[pino].poc.Precno0 = tcp.PrcT
					pis[pino].poc.Precno1 = tcp.PrcT + 1
					tcp.PrcT++
				default:
					tcp.Tx0T = tcp.TxS
					tcp.Ty0T = tcp.TyS
					pis[pino].poc.Tx0 = tcp.Tx0T
					pis[pino].poc.Tx1 = tcp.Tx0T + tcp.Dx - (tcp.Tx0T % tcp.Dx)
					pis[pino].poc.Ty0 = tcp.Ty0T
					pis[pino].poc.Ty1 = tcp.Ty0T + tcp.Dy - (tcp.Ty0T % tcp.Dy)
					tcp.Tx0T = pis[pino].poc.Tx1
					tcp.Ty0T = pis[pino].poc.Ty1
				}
			}
		}
		return
	}

	incrTop := uint32(1)
	resetX := uint32(0)
	for i := tppos; i >= 0; i-- {
		switch prog[i] {
		case 'C':
			pis[pino].poc.Compno0 = tcp.CompT - 1
			pis[pino].poc.Compno1 = tcp.CompT
		case 'R':
			pis[pino].poc.Resno0 = tcp.ResT - 1
			pis[pino].poc.Resno1 = tcp.ResT
		case 'L':
			pis[pino].poc.Layno0 = tcp.LayT - 1
			pis[pino].poc.Layno1 = tcp.LayT
		case 'P':
			switch tcp.Prg {
			case cparams.LRCP, cparams.RLCP:
				pis[pino].poc.Precno0 = tcp.PrcT - 1
				pis[pino].poc.Precno1 = tcp.PrcT
			default:
				pis[pino].poc.Tx0 = tcp.Tx0T - tcp.Dx - (tcp.Tx0T % tcp.Dx)
				pis[pino].poc.Tx1 = tcp.Tx0T
				pis[pino].poc.Ty0 = tcp.Ty0T - tcp.Dy - (tcp.Ty0T % tcp.Dy)
				pis[pino].poc.Ty1 = tcp.Ty0T
			}
		}
		if incrTop == 1 {
			switch prog[i] {
			case 'R':
				if tcp.ResT == tcp.ResE {
					if checkNextLevel(i-1, cp, tileno, pino, prog) {
						tcp.ResT = tcp.ResS
						pis[pino].poc.Resno0 = tcp.ResT
						pis[pino].poc.Resno1 = tcp.ResT + 1
						tcp.ResT++
						incrTop = 1
					} else {
						incrTop = 0
					}
				} else {
					pis[pino].poc.Resno0 = tcp.ResT
					pis[pino].poc.Resno1 = tcp.ResT + 1
					tcp.ResT++
					incrTop = 0
				}
			case 'C':
				if tcp.CompT == tcp.CompE {
					if checkNextLevel(i-1, cp, tileno, pino, prog) {
						tcp.CompT = tcp.CompS
						pis[pino].poc.Compno0 = tcp.CompT
						pis[pino].poc.Compno1 = tcp.CompT + 1
						tcp.CompT++
						incrTop = 1
					} else {
						incrTop = 0
					}
				} else {
					pis[pino].poc.Compno0 = tcp.CompT
					pis[pino].poc.Compno1 = tcp.CompT + 1
					tcp.CompT++
					incrTop = 0
				}
			case 'L':
				if tcp.LayT == tcp.LayE {
					if checkNextLevel(i-1, cp, tileno, pino, prog) {
						tcp.LayT = tcp.LayS
						pis[pino].poc.Layno0 = tcp.LayT
						pis[pino].poc.Layno1 = tcp.LayT + 1
						tcp.LayT++
						incrTop = 1
					} else {
						incrTop = 0
					}
				} else {
					pis[pino].poc.Layno0 = tcp.LayT
					pis[pino].poc.Layno1 = tcp.LayT + 1
					tcp.LayT++
					incrTop = 0
				}
			case 'P':
				switch tcp.Prg {
				case cparams.LRCP, cparams.RLCP:
					if tcp.PrcT == tcp.PrcE {
						if checkNextLevel(i-1, cp, tileno, pino, prog) {
							tcp.PrcT = tcp.PrcS
							pis[pino].poc.Precno0 = tcp.PrcT
							pis[pino].poc.Precno1 = tcp.PrcT + 1
							tcp.PrcT++
							incrTop = 1
						} else {
							incrTop = 0
						}
					} else {
						pis[pino].poc.Precno0 = tcp.PrcT
						pis[pino].poc.Precno1 = tcp.PrcT + 1
						tcp.PrcT++
						incrTop = 0
					}
				default:
					if tcp.Tx0T >= tcp.TxE {
						if tcp.Ty0T >= tcp.TyE {
							if checkNextLevel(i-1, cp, tileno, pino, prog) {
								tcp.Ty0T = tcp.TyS
								pis[pino].poc.Ty0 = tcp.Ty0T
								pis[pino].poc.Ty1 = tcp.Ty0T + tcp.Dy - (tcp.Ty0T % tcp.Dy)
								tcp.Ty0T = pis[pino].poc.Ty1
								incrTop = 1
								resetX = 1
							} else {
								incrTop = 0
								resetX = 0
							}
						} else {
							pis[pino].poc.Ty0 = tcp.Ty0T
							pis[pino].poc.Ty1 = tcp.Ty0T + tcp.Dy - (tcp.Ty0T % tcp.Dy)
							tcp.Ty0T = pis[pino].poc.Ty1
							incrTop = 0
							resetX = 1
						}
						if resetX == 1 {
							tcp.Tx0T = tcp.TxS
							pis[pino].poc.Tx0 = tcp.Tx0T
							pis[pino].poc.Tx1 = tcp.Tx0T + tcp.Dx - (tcp.Tx0T % tcp.Dx)
							tcp.Tx0T = pis[pino].poc.Tx1
						}
					} else {
						pis[pino].poc.Tx0 = tcp.Tx0T
						pis[pino].poc.Tx1 = tcp.Tx0T + tcp.Dx - (tcp.Tx0T % tcp.Dx)
						tcp.Tx0T = pis[pino].poc.Tx1
						incrTop = 0
					}
				}
			}
		}
	}
}

// UpdateEncodingParameters ports opj_pi_update_encoding_parameters.
func UpdateEncodingParameters(img *image.Image, cp *cparams.CP, tileno uint32) {
	tcp := &cp.Tcps[tileno]
	tx0, tx1, ty0, ty1, dxMin, dyMin, maxPrec, maxRes := getEncodingParameters(img, cp, tileno)
	if tcp.POC != 0 {
		updateEncodePocAndFinal(cp, tileno, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin)
	} else {
		updateEncodeNotPoc(cp, img.Numcomps, tileno, tx0, tx1, ty0, ty1, maxPrec, maxRes, dxMin, dyMin)
	}
}
