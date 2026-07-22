package tcd

// Rate allocation, port of the encode-side rate control in tcd.c:
// opj_tcd_rate_allocate_encode, opj_tcd_rateallocate (bisection over
// distortion/rate thresholds), opj_tcd_rateallocate_fixed, opj_tcd_makelayer
// and opj_tcd_makelayer_fixed. Float64 accumulators and float math order match
// the C reference exactly, since layer truncation drives byte-identity.

import (
	"math"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/t2"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// rateAllocateEncode ports opj_tcd_rate_allocate_encode.
func (t *TCD) rateAllocateEncode(dest []byte, maxDestSize uint32, mgr *event.Manager) error {
	cp := t.CP
	strat := cp.MEnc.MQualityLayerAllocStrategy
	if strat == cparams.RateDistortionRatio || strat == cparams.FixedDistortionRatio {
		if !t.rateallocate(dest, maxDestSize, mgr) {
			return errTierDecode
		}
	} else {
		t.rateallocateFixed()
	}
	return nil
}

// rateallocateFixed ports opj_tcd_rateallocate_fixed.
func (t *TCD) rateallocateFixed() {
	for layno := uint32(0); layno < t.TCP.Numlayers; layno++ {
		t.makelayerFixed(layno, 1)
	}
}

// makelayer ports opj_tcd_makelayer. Returns whether the layer allocation is
// unchanged versus the previous invocation with a different threshold.
func (t *TCD) makelayer(layno uint32, thresh float64, final uint32) bool {
	tcdTile := t.tile()
	layerAllocationIsSame := true
	tcdTile.Distolayer[layno] = 0

	for compno := uint32(0); compno < tcdTile.Numcomps; compno++ {
		tilec := &tcdTile.Comps[compno]
		for resno := uint32(0); resno < tilec.Numresolutions; resno++ {
			res := &tilec.Resolutions[resno]
			for bandno := uint32(0); bandno < res.Numbands; bandno++ {
				band := &res.Bands[bandno]
				if tile.IsBandEmpty(band) {
					continue
				}
				for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
					prc := &band.Precincts[precno]
					for cblkno := uint32(0); cblkno < prc.Cw*prc.Ch; cblkno++ {
						cblk := &prc.CblksEnc[cblkno]
						layer := &cblk.Layers[layno]

						if layno == 0 {
							cblk.Numpassesinlayers = 0
						}
						n := cblk.Numpassesinlayers

						if thresh < 0 {
							n = cblk.Totalpasses
						} else {
							for passno := cblk.Numpassesinlayers; passno < cblk.Totalpasses; passno++ {
								var dr uint32
								var dd float64
								pass := &cblk.Passes[passno]
								if n == 0 {
									dr = pass.Rate
									dd = pass.Distortiondec
								} else {
									dr = pass.Rate - cblk.Passes[n-1].Rate
									dd = pass.Distortiondec - cblk.Passes[n-1].Distortiondec
								}
								if dr == 0 {
									if dd != 0 {
										n = passno + 1
									}
									continue
								}
								if thresh-(dd/float64(dr)) < dblEpsilon {
									n = passno + 1
								}
							}
						}

						if layer.Numpasses != n-cblk.Numpassesinlayers {
							layerAllocationIsSame = false
							layer.Numpasses = n - cblk.Numpassesinlayers
						}

						if layer.Numpasses == 0 {
							layer.Disto = 0
							continue
						}

						if cblk.Numpassesinlayers == 0 {
							layer.Len = cblk.Passes[n-1].Rate
							layer.Data = cblk.Data
							layer.Disto = cblk.Passes[n-1].Distortiondec
						} else {
							off := cblk.Passes[cblk.Numpassesinlayers-1].Rate
							layer.Len = cblk.Passes[n-1].Rate - off
							layer.Data = cblk.Data[off:]
							layer.Disto = cblk.Passes[n-1].Distortiondec -
								cblk.Passes[cblk.Numpassesinlayers-1].Distortiondec
						}

						tcdTile.Distolayer[layno] += layer.Disto

						if final != 0 {
							cblk.Numpassesinlayers = n
						}
					}
				}
			}
		}
	}
	return layerAllocationIsSame
}

// makelayerFixed ports opj_tcd_makelayer_fixed (FIXED_LAYER strategy).
func (t *TCD) makelayerFixed(layno uint32, final uint32) {
	cp := t.CP
	tcdTile := t.tile()
	tcdTcp := t.TCP

	var matrice [cparams.TCDMatrixMaxLayerCount][cparams.TCDMatrixMaxResolutionCount][3]int32

	for compno := uint32(0); compno < tcdTile.Numcomps; compno++ {
		tilec := &tcdTile.Comps[compno]
		prec := t.Image.Comps[compno].Prec
		for i := uint32(0); i < tcdTcp.Numlayers; i++ {
			for j := uint32(0); j < tilec.Numresolutions; j++ {
				for k := uint32(0); k < 3; k++ {
					idx := i*tilec.Numresolutions*3 + j*3 + k
					matrice[i][j][k] = int32(float32(cp.MEnc.MMatrice[idx]) *
						float32(float64(prec)/16.0))
				}
			}
		}

		for resno := uint32(0); resno < tilec.Numresolutions; resno++ {
			res := &tilec.Resolutions[resno]
			for bandno := uint32(0); bandno < res.Numbands; bandno++ {
				band := &res.Bands[bandno]
				if tile.IsBandEmpty(band) {
					continue
				}
				for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
					prc := &band.Precincts[precno]
					for cblkno := uint32(0); cblkno < prc.Cw*prc.Ch; cblkno++ {
						cblk := &prc.CblksEnc[cblkno]
						layer := &cblk.Layers[layno]
						imsb := int32(prec) - int32(cblk.Numbps)

						var value int32
						if layno == 0 {
							value = matrice[layno][resno][bandno]
							if imsb >= value {
								value = 0
							} else {
								value -= imsb
							}
						} else {
							value = matrice[layno][resno][bandno] - matrice[layno-1][resno][bandno]
							if imsb >= matrice[layno-1][resno][bandno] {
								value -= imsb - matrice[layno-1][resno][bandno]
								if value < 0 {
									value = 0
								}
							}
						}

						if layno == 0 {
							cblk.Numpassesinlayers = 0
						}
						n := cblk.Numpassesinlayers
						if cblk.Numpassesinlayers == 0 {
							if value != 0 {
								n = 3*uint32(value) - 2 + cblk.Numpassesinlayers
							} else {
								n = cblk.Numpassesinlayers
							}
						} else {
							n = 3*uint32(value) + cblk.Numpassesinlayers
						}

						layer.Numpasses = n - cblk.Numpassesinlayers
						if layer.Numpasses == 0 {
							continue
						}

						if cblk.Numpassesinlayers == 0 {
							layer.Len = cblk.Passes[n-1].Rate
							layer.Data = cblk.Data
						} else {
							off := cblk.Passes[cblk.Numpassesinlayers-1].Rate
							layer.Len = cblk.Passes[n-1].Rate - off
							layer.Data = cblk.Data[off:]
						}

						if final != 0 {
							cblk.Numpassesinlayers = n
						}
					}
				}
			}
		}
	}
}

// dblEpsilon ports DBL_EPSILON.
const dblEpsilon = 2.2204460492503131e-16

// rateallocate ports opj_tcd_rateallocate.
func (t *TCD) rateallocate(dest []byte, length uint32, mgr *event.Manager) bool {
	cp := t.CP
	tcdTile := t.tile()
	tcdTcp := t.TCP
	strat := cp.MEnc.MQualityLayerAllocStrategy

	min := math.MaxFloat64
	max := 0.0
	const K = 1.0
	maxSE := 0.0

	var cumdisto [100]float64

	tcdTile.Numpix = 0

	for compno := uint32(0); compno < tcdTile.Numcomps; compno++ {
		tilec := &tcdTile.Comps[compno]
		tilec.Numpix = 0
		for resno := uint32(0); resno < tilec.Numresolutions; resno++ {
			res := &tilec.Resolutions[resno]
			for bandno := uint32(0); bandno < res.Numbands; bandno++ {
				band := &res.Bands[bandno]
				if tile.IsBandEmpty(band) {
					continue
				}
				for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
					prc := &band.Precincts[precno]
					for cblkno := uint32(0); cblkno < prc.Cw*prc.Ch; cblkno++ {
						cblk := &prc.CblksEnc[cblkno]
						for passno := uint32(0); passno < cblk.Totalpasses; passno++ {
							pass := &cblk.Passes[passno]
							var dr int32
							var dd float64
							if passno == 0 {
								dr = int32(pass.Rate)
								dd = pass.Distortiondec
							} else {
								dr = int32(pass.Rate - cblk.Passes[passno-1].Rate)
								dd = pass.Distortiondec - cblk.Passes[passno-1].Distortiondec
							}
							if dr == 0 {
								continue
							}
							rdslope := dd / float64(dr)
							if rdslope < min {
								min = rdslope
							}
							if rdslope > max {
								max = rdslope
							}
						}
						cblkPixCount := uint64((cblk.X1 - cblk.X0) * (cblk.Y1 - cblk.Y0))
						tcdTile.Numpix += cblkPixCount
						tilec.Numpix += cblkPixCount
					}
				}
			}
		}
		precF := float64(uint64(1) << t.Image.Comps[compno].Prec)
		maxSE += (precF - 1.0) * (precF - 1.0) * float64(tilec.Numpix)
	}

	for layno := uint32(0); layno < tcdTcp.Numlayers; layno++ {
		lo := min
		hi := max
		var maxlen uint32
		if tcdTcp.Rates[layno] > 0.0 {
			maxlen = uintMin(uint32(math.Ceil(float64(tcdTcp.Rates[layno]))), length)
		} else {
			maxlen = length
		}
		goodthresh := 0.0
		stableThresh := 0.0

		distotarget := tcdTile.Distotile -
			((K * maxSE) / math.Pow(10, float64(tcdTcp.Distoratio[layno]/10)))

		if (strat == cparams.RateDistortionRatio && tcdTcp.Rates[layno] > 0.0) ||
			(strat == cparams.FixedDistortionRatio && tcdTcp.Distoratio[layno] > 0.0) {
			engine := t2.Create(t.Image, cp)
			thresh := 0.0
			lastLayerAllocationOk := false

			for i := 0; i < 128; i++ {
				newThresh := (lo + hi) / 2
				if math.Abs(newThresh-thresh) <= 0.5*1e-5*thresh {
					break
				}
				thresh = newThresh

				layerAllocationIsSame := t.makelayer(layno, thresh, 0) && i != 0

				if strat == cparams.FixedDistortionRatio {
					_ = layerAllocationIsSame
					if cparams.IsCinema(cp.Rsiz) || cparams.IsIMF(cp.Rsiz) {
						_, ok := engine.EncodePackets(t.TcdTileno, tcdTile, layno+1, dest,
							maxlen, nil, nil, t.CurTpNum, t.TpPos, t.CurPino,
							cparams.ThreshCalc, mgr)
						if !ok {
							lo = thresh
							continue
						}
						var distoachieved float64
						if layno == 0 {
							distoachieved = tcdTile.Distolayer[0]
						} else {
							distoachieved = cumdisto[layno-1] + tcdTile.Distolayer[layno]
						}
						if distoachieved < distotarget {
							hi = thresh
							stableThresh = thresh
							continue
						}
						lo = thresh
					} else {
						var distoachieved float64
						if layno == 0 {
							distoachieved = tcdTile.Distolayer[0]
						} else {
							distoachieved = cumdisto[layno-1] + tcdTile.Distolayer[layno]
						}
						if distoachieved < distotarget {
							hi = thresh
							stableThresh = thresh
							continue
						}
						lo = thresh
					}
				} else { // disto/rate based optimization
					if (layerAllocationIsSame && !lastLayerAllocationOk) ||
						func() bool {
							if layerAllocationIsSame {
								return false
							}
							_, ok := engine.EncodePackets(t.TcdTileno, tcdTile, layno+1, dest,
								maxlen, nil, nil, t.CurTpNum, t.TpPos, t.CurPino,
								cparams.ThreshCalc, mgr)
							return !ok
						}() {
						lastLayerAllocationOk = false
						lo = thresh
						continue
					}
					lastLayerAllocationOk = true
					hi = thresh
					stableThresh = thresh
				}
			}

			if stableThresh == 0 {
				goodthresh = thresh
			} else {
				goodthresh = stableThresh
			}
		} else {
			goodthresh = -1
		}

		t.makelayer(layno, goodthresh, 1)

		if layno == 0 {
			cumdisto[layno] = tcdTile.Distolayer[0]
		} else {
			cumdisto[layno] = cumdisto[layno-1] + tcdTile.Distolayer[layno]
		}
	}

	return true
}

// uintMin ports opj_uint_min.
func uintMin(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
