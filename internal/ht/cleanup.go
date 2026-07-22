package ht

import (
	"math/bits"

	"github.com/mgilbir/gopenjpeg/internal/event"
)

// decodeCleanup ports the body of opj_t1_ht_decode_cblk after initialization:
// the cleanup pass (initial two rows + non-initial rows), the interleaved
// SigProp (SPP) and MagRef (MRP) refinement passes, and their terminating
// stripe. It writes reconstructed sign+magnitude samples into d.data.
//
// Returns a non-nil error for the two in-loop malformed conditions where the C
// reference returns OPJ_FALSE (U_q overflow, or significant samples outside the
// block); nil on success.
func (d *Decoder) decodeCleanup(
	mel *decMel, vlc, magref *revStruct, magsgn, sigprop *frwdStruct,
	width, height, stride int, p, zeroBplanesP1, numPasses uint32,
	stripeCausal bool, em *event.Manager,
) error {
	data := d.data
	sigma1, sigma2 := d.sigma1, d.sigma2
	mbr1, mbr2 := d.mbr1, d.mbr2
	ls := d.lineState

	sipArr := sigma1
	sipIdx := 0
	sipShift := uint32(0)

	// ---- initial 2 lines ----
	ls[0] = 0
	run := mel.getRun()
	var qinf [2]uint32
	cQ := uint32(0)

	for x := 0; x < width; x += 4 {
		var Uq [2]uint32

		vlcVal := vlc.fetch()

		// first quad
		qinf[0] = uint32(vlcTbl0[(cQ<<7)|(vlcVal&0x7F)])
		if cQ == 0 {
			run -= 2
			if run != -1 {
				qinf[0] = 0
			}
			if run < 0 {
				run = mel.getRun()
			}
		}
		cQ = ((qinf[0] & 0x10) >> 4) | ((qinf[0] & 0xE0) >> 5)
		vlcVal = vlc.advance(qinf[0] & 0x7)

		sipArr[sipIdx] |= (((qinf[0] & 0x30) >> 4) | ((qinf[0] & 0xC0) >> 2)) << sipShift

		// second quad
		qinf[1] = 0
		if x+2 < width {
			qinf[1] = uint32(vlcTbl0[(cQ<<7)|(vlcVal&0x7F)])
			if cQ == 0 {
				run -= 2
				if run != -1 {
					qinf[1] = 0
				}
				if run < 0 {
					run = mel.getRun()
				}
			}
			cQ = ((qinf[1] & 0x10) >> 4) | ((qinf[1] & 0xE0) >> 5)
			vlcVal = vlc.advance(qinf[1] & 0x7)
		}

		sipArr[sipIdx] |= ((qinf[1] & 0x30) | ((qinf[1] & 0xC0) << 2)) << (4 + sipShift)
		if x&0x7 != 0 {
			sipIdx++
		}
		sipShift ^= 0x10

		// retrieve u
		uvlcMode := ((qinf[0] & 0x8) >> 3) | ((qinf[1] & 0x8) >> 2)
		if uvlcMode == 3 {
			run -= 2
			if run == -1 {
				uvlcMode++
			}
			if run < 0 {
				run = mel.getRun()
			}
		}
		consumed := decodeInitUVLC(vlcVal, uvlcMode, &Uq)
		if Uq[0] > zeroBplanesP1 || Uq[1] > zeroBplanesP1 {
			return failErr(em, "Malformed HT codeblock. Decoding this codeblock "+
				"is stopped. U_q is larger than zero bitplanes + 1 \n")
		}
		vlcVal = vlc.advance(consumed)

		// locations that need evaluation
		locs := uint32(0xFF)
		if x+4 > width {
			locs >>= uint((x + 4 - width) << 1)
		}
		if height <= 1 {
			locs &= 0x55
		}
		if ((((qinf[0] & 0xF0) >> 4) | (qinf[1] & 0xF0)) & ^locs) != 0 {
			return failErr(em, "Malformed HT codeblock. VLC code produces "+
				"significant samples outside the codeblock area.\n")
		}

		d.decodeSamples(magsgn, qinf[0], qinf[1], Uq[0], Uq[1], p, locs, x, 2*(x>>2), stride)
	}

	// ---- non-initial lines ----
	for y := 2; y < height; {
		sipShift ^= 0x2
		sipShift &= 0xFFFFFFEF
		if y&0x4 != 0 {
			sipArr = sigma2
		} else {
			sipArr = sigma1
		}
		sipIdx = 0

		lspIdx := 0
		ls0 := ls[0]
		ls[0] = 0
		spRow := y * stride
		cQ = 0

		for x := 0; x < width; x += 4 {
			var Uq [2]uint32

			// first quad context (eqn. 2)
			cQ |= uint32(ls0 >> 7)
			cQ |= (uint32(ls[lspIdx+1]) >> 5) & 0x4

			vlcVal := vlc.fetch()
			qinf[0] = uint32(vlcTbl1[(cQ<<7)|(vlcVal&0x7F)])
			if cQ == 0 {
				run -= 2
				if run != -1 {
					qinf[0] = 0
				}
				if run < 0 {
					run = mel.getRun()
				}
			}
			cQ = ((qinf[0] & 0x40) >> 5) | ((qinf[0] & 0x80) >> 6)
			vlcVal = vlc.advance(qinf[0] & 0x7)

			sipArr[sipIdx] |= (((qinf[0] & 0x30) >> 4) | ((qinf[0] & 0xC0) >> 2)) << sipShift

			// second quad
			qinf[1] = 0
			if x+2 < width {
				cQ |= uint32(ls[lspIdx+1] >> 7)
				cQ |= (uint32(ls[lspIdx+2]) >> 5) & 0x4
				qinf[1] = uint32(vlcTbl1[(cQ<<7)|(vlcVal&0x7F)])
				if cQ == 0 {
					run -= 2
					if run != -1 {
						qinf[1] = 0
					}
					if run < 0 {
						run = mel.getRun()
					}
				}
				cQ = ((qinf[1] & 0x40) >> 5) | ((qinf[1] & 0x80) >> 6)
				vlcVal = vlc.advance(qinf[1] & 0x7)
			}

			sipArr[sipIdx] |= ((qinf[1] & 0x30) | ((qinf[1] & 0xC0) << 2)) << (4 + sipShift)
			if x&0x7 != 0 {
				sipIdx++
			}
			sipShift ^= 0x10

			// retrieve u
			uvlcMode := ((qinf[0] & 0x8) >> 3) | ((qinf[1] & 0x8) >> 2)
			consumed := decodeNoninitUVLC(vlcVal, uvlcMode, &Uq)
			vlcVal = vlc.advance(consumed)

			// E^max, eqns 5 and 6
			if q := qinf[0] & 0xF0; q&(q-1) != 0 {
				E := uint32(ls0) & 0x7F
				E = max(E, uint32(ls[lspIdx+1])&0x7F)
				if E > 2 {
					Uq[0] += E - 2
				}
			}
			if q := qinf[1] & 0xF0; q&(q-1) != 0 {
				E := uint32(ls[lspIdx+1]) & 0x7F
				E = max(E, uint32(ls[lspIdx+2])&0x7F)
				if E > 2 {
					Uq[1] += E - 2
				}
			}

			if Uq[0] > zeroBplanesP1 || Uq[1] > zeroBplanesP1 {
				return failErr(em, "Malformed HT codeblock. Decoding this "+
					"codeblock is stopped. U_q islarger than bitplanes + 1 \n")
			}

			ls0 = ls[lspIdx+2]
			ls[lspIdx+1] = 0
			ls[lspIdx+2] = 0

			locs := uint32(0xFF)
			if x+4 > width {
				locs >>= uint((x + 4 - width) << 1)
			}
			if y+2 > height {
				locs &= 0x55
			}
			if ((((qinf[0] & 0xF0) >> 4) | (qinf[1] & 0xF0)) & ^locs) != 0 {
				return failErr(em, "Malformed HT codeblock. VLC code produces "+
					"significant samples outside the codeblock area.\n")
			}

			d.decodeSamples(magsgn, qinf[0], qinf[1], Uq[0], Uq[1], p, locs, spRow+x, lspIdx, stride)
			lspIdx += 2
		}

		y += 2
		if numPasses > 1 && (y&3) == 0 {
			// SPP and potentially MRP
			if numPasses > 2 { // MRP
				var curSig []uint32
				if y&0x4 != 0 {
					curSig = sigma1
				} else {
					curSig = sigma2
				}
				dpp := (y - 4) * stride
				half := uint32(1) << (p - 2)
				csIdx := 0
				for i := 0; i < width; i += 8 {
					cwd := magref.fetch()
					sig := curSig[csIdx]
					csIdx++
					colMask := uint32(0xF)
					dp := dpp + i
					if sig != 0 {
						for j := 0; j < 8; j++ {
							if sig&colMask != 0 {
								sampleMask := 0x11111111 & colMask
								if sig&sampleMask != 0 {
									sym := cwd & 1
									data[dp] ^= (1 - sym) << (p - 1)
									data[dp] |= half
									cwd >>= 1
								}
								sampleMask += sampleMask
								if sig&sampleMask != 0 {
									sym := cwd & 1
									data[dp+stride] ^= (1 - sym) << (p - 1)
									data[dp+stride] |= half
									cwd >>= 1
								}
								sampleMask += sampleMask
								if sig&sampleMask != 0 {
									sym := cwd & 1
									data[dp+2*stride] ^= (1 - sym) << (p - 1)
									data[dp+2*stride] |= half
									cwd >>= 1
								}
								sampleMask += sampleMask
								if sig&sampleMask != 0 {
									sym := cwd & 1
									data[dp+3*stride] ^= (1 - sym) << (p - 1)
									data[dp+3*stride] |= half
									cwd >>= 1
								}
							}
							colMask <<= 4
							dp++
						}
					}
					magref.advance(uint32(bits.OnesCount32(sig)))
				}
			}

			if y >= 4 { // update mbr array at end of each stripe
				var sig, mbr []uint32
				if y&0x4 != 0 {
					sig, mbr = sigma1, mbr1
				} else {
					sig, mbr = sigma2, mbr2
				}
				prev := uint32(0)
				k := 0
				for i := 0; i < width; i += 8 {
					mbr[k] = sig[k]
					mbr[k] |= prev >> 28
					mbr[k] |= sig[k] << 4
					mbr[k] |= sig[k] >> 4
					mbr[k] |= sig[k+1] << 28
					prev = sig[k]

					t := mbr[k]
					z := mbr[k]
					z |= (t & 0x77777777) << 1
					z |= (t & 0xEEEEEEEE) >> 1
					mbr[k] = z & ^sig[k]
					k++
				}
			}

			if y >= 8 {
				d.sigProp(sigprop, sigma1, sigma2, mbr1, mbr2, width, y, stride, p, stripeCausal)

				// clear current sigma
				var curSig []uint32
				if y&0x4 != 0 {
					curSig = sigma2
				} else {
					curSig = sigma1
				}
				n := (((width + 7) >> 3) + 1)
				for i := 0; i < n; i++ {
					curSig[i] = 0
				}
			}
		}
	}

	// ---- terminating ----
	if numPasses > 1 {
		if numPasses > 2 && ((height&3) == 1 || (height&3) == 2) {
			// MRP on the last incomplete stripe
			var curSig []uint32
			if height&0x4 != 0 {
				curSig = sigma2
			} else {
				curSig = sigma1
			}
			dpp := (height & 0xFFFFFC) * stride
			half := uint32(1) << (p - 2)
			csIdx := 0
			for i := 0; i < width; i += 8 {
				cwd := magref.fetch()
				sig := curSig[csIdx]
				csIdx++
				colMask := uint32(0xF)
				dp := dpp + i
				if sig != 0 {
					for j := 0; j < 8; j++ {
						if sig&colMask != 0 {
							sampleMask := 0x11111111 & colMask
							if sig&sampleMask != 0 {
								sym := cwd & 1
								data[dp] ^= (1 - sym) << (p - 1)
								data[dp] |= half
								cwd >>= 1
							}
							sampleMask += sampleMask
							if sig&sampleMask != 0 {
								sym := cwd & 1
								data[dp+stride] ^= (1 - sym) << (p - 1)
								data[dp+stride] |= half
								cwd >>= 1
							}
							sampleMask += sampleMask
							if sig&sampleMask != 0 {
								sym := cwd & 1
								data[dp+2*stride] ^= (1 - sym) << (p - 1)
								data[dp+2*stride] |= half
								cwd >>= 1
							}
							sampleMask += sampleMask
							if sig&sampleMask != 0 {
								sym := cwd & 1
								data[dp+3*stride] ^= (1 - sym) << (p - 1)
								data[dp+3*stride] |= half
								cwd >>= 1
							}
						}
						colMask <<= 4
						dp++
					}
				}
				magref.advance(uint32(bits.OnesCount32(sig)))
			}
		}

		// last incomplete stripe mbr (for (height&3) == 1 or 2)
		if (height&3) == 1 || (height&3) == 2 {
			var sig, mbr []uint32
			if height&0x4 != 0 {
				sig, mbr = sigma2, mbr2
			} else {
				sig, mbr = sigma1, mbr1
			}
			prev := uint32(0)
			k := 0
			for i := 0; i < width; i += 8 {
				mbr[k] = sig[k]
				mbr[k] |= prev >> 28
				mbr[k] |= sig[k] << 4
				mbr[k] |= sig[k] >> 4
				mbr[k] |= sig[k+1] << 28
				prev = sig[k]

				t := mbr[k]
				z := mbr[k]
				z |= (t & 0x77777777) << 1
				z |= (t & 0xEEEEEEEE) >> 1
				mbr[k] = z & ^sig[k]
				k++
			}
		}

		st := height
		if height > 6 {
			st -= ((height + 1) & 3) + 3
		} else {
			st -= height
		}
		for y := st; y < height; y += 4 {
			d.sigPropTerm(sigprop, sigma1, sigma2, mbr1, mbr2, width, y, height, stride, p, stripeCausal)
		}
	}

	return nil
}

// sigProp ports the y>=8 SigProp block executed inside the non-initial-rows
// loop of opj_t1_ht_decode_cblk (finding newly significant samples in the
// stripe two above the current one and decoding their signs).
func (d *Decoder) sigProp(
	sigprop *frwdStruct, sigma1, sigma2, mbr1, mbr2 []uint32,
	width, y, stride int, p uint32, stripeCausal bool,
) {
	data := d.data

	// add membership from the next stripe
	var curSig, curMbr, nxtSig []uint32
	if y&0x4 != 0 {
		curSig, curMbr, nxtSig = sigma2, mbr2, sigma1
	} else {
		curSig, curMbr, nxtSig = sigma1, mbr1, sigma2
	}
	prev := uint32(0)
	k := 0
	for i := 0; i < width; i += 8 {
		t := nxtSig[k]
		t |= prev >> 28
		t |= nxtSig[k] << 4
		t |= nxtSig[k] >> 4
		t |= nxtSig[k+1] << 28
		prev = nxtSig[k]
		if !stripeCausal {
			curMbr[k] |= (t & 0x11111111) << 3
		}
		curMbr[k] &= ^curSig[k]
		k++
	}

	// find new locations and get signs
	var nxtMbr []uint32
	if y&0x4 != 0 {
		curSig, curMbr, nxtSig, nxtMbr = sigma2, mbr2, sigma1, mbr1
	} else {
		curSig, curMbr, nxtSig, nxtMbr = sigma1, mbr1, sigma2, mbr2
	}
	val := uint32(3) << (p - 2)
	k = 0
	for i := 0; i < width; i += 8 {
		mbr := curMbr[k]
		newSig := uint32(0)
		if mbr != 0 {
			for n := 0; n < 8; n += 4 {
				cwd := sigprop.fetch()
				cnt := uint32(0)
				dpBase := (y-8)*stride + i + n
				colMask := uint32(0xF) << uint(4*n)
				invSig := ^curSig[k]
				end := n + 4
				if n+4+i >= width {
					end = width - i
				}
				dp := dpBase
				for j := n; j < end; j++ {
					if colMask&mbr != 0 {
						sampleMask := 0x11111111 & colMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0x32) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0x74) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0xE8) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0xC0) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
					}
					colMask <<= 4
					dp++
				}

				// obtain signs
				if newSig&(0xFFFF<<uint(4*n)) != 0 {
					colMask = uint32(0xF) << uint(4*n)
					dp = dpBase
					for j := n; j < end; j++ {
						if colMask&newSig != 0 {
							sampleMask := 0x11111111 & colMask
							if newSig&sampleMask != 0 {
								data[dp] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+2*stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+3*stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
						}
						colMask <<= 4
						dp++
					}
				}
				sigprop.advance(cnt)

				// update the next 8 columns
				if n == 4 {
					t := newSig >> 28
					t |= ((t & 0xE) >> 1) | ((t & 7) << 1)
					curMbr[k+1] |= t & ^curSig[k+1]
				}
			}
		}
		// vertical propagation to the next stripe
		newSig |= curSig[k]
		ux := (newSig & 0x88888888) >> 3
		tx := ux | (ux << 4) | (ux >> 4)
		if i > 0 {
			nxtMbr[k-1] |= (ux << 28) & ^nxtSig[k-1]
		}
		nxtMbr[k] |= tx & ^nxtSig[k]
		nxtMbr[k+1] |= (ux >> 28) & ^nxtSig[k+1]
		k++
	}
}

// sigPropTerm ports the terminating SigProp stripe loop (the y in [st,height)
// loop) of opj_t1_ht_decode_cblk, with the height-dependent pattern masking.
func (d *Decoder) sigPropTerm(
	sigprop *frwdStruct, sigma1, sigma2, mbr1, mbr2 []uint32,
	width, y, height, stride int, p uint32, stripeCausal bool,
) {
	data := d.data

	pattern := uint32(0xFFFFFFFF)
	switch height - y {
	case 3:
		pattern = 0x77777777
	case 2:
		pattern = 0x33333333
	case 1:
		pattern = 0x11111111
	}

	// add membership from the next stripe (only when height-y > 4)
	if height-y > 4 {
		var curSig, curMbr, nxtSig []uint32
		if y&0x4 != 0 {
			curSig, curMbr, nxtSig = sigma2, mbr2, sigma1
		} else {
			curSig, curMbr, nxtSig = sigma1, mbr1, sigma2
		}
		prev := uint32(0)
		k := 0
		for i := 0; i < width; i += 8 {
			t := nxtSig[k]
			t |= prev >> 28
			t |= nxtSig[k] << 4
			t |= nxtSig[k] >> 4
			t |= nxtSig[k+1] << 28
			prev = nxtSig[k]
			if !stripeCausal {
				curMbr[k] |= (t & 0x11111111) << 3
			}
			curMbr[k] &= ^curSig[k]
			k++
		}
	}

	// find new locations and get signs
	var curSig, curMbr, nxtSig, nxtMbr []uint32
	if y&0x4 != 0 {
		curSig, curMbr, nxtSig, nxtMbr = sigma2, mbr2, sigma1, mbr1
	} else {
		curSig, curMbr, nxtSig, nxtMbr = sigma1, mbr1, sigma2, mbr2
	}
	val := uint32(3) << (p - 2)
	k := 0
	for i := 0; i < width; i += 8 {
		mbr := curMbr[k] & pattern
		newSig := uint32(0)
		if mbr != 0 {
			for n := 0; n < 8; n += 4 {
				cwd := sigprop.fetch()
				cnt := uint32(0)
				dpBase := y*stride + i + n
				colMask := uint32(0xF) << uint(4*n)
				invSig := ^curSig[k] & pattern
				end := n + 4
				if n+4+i >= width {
					end = width - i
				}
				dp := dpBase
				for j := n; j < end; j++ {
					if colMask&mbr != 0 {
						sampleMask := 0x11111111 & colMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0x32) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0x74) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0xE8) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
						sampleMask += sampleMask
						if mbr&sampleMask != 0 {
							if cwd&1 != 0 {
								newSig |= sampleMask
								t := uint32(0xC0) << uint(j*4)
								mbr |= t & invSig
							}
							cwd >>= 1
							cnt++
						}
					}
					colMask <<= 4
					dp++
				}

				if newSig&(0xFFFF<<uint(4*n)) != 0 {
					colMask = uint32(0xF) << uint(4*n)
					dp = dpBase
					for j := n; j < end; j++ {
						if colMask&newSig != 0 {
							sampleMask := 0x11111111 & colMask
							if newSig&sampleMask != 0 {
								data[dp] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+2*stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
							sampleMask += sampleMask
							if newSig&sampleMask != 0 {
								data[dp+3*stride] |= ((cwd & 1) << 31) | val
								cwd >>= 1
								cnt++
							}
						}
						colMask <<= 4
						dp++
					}
				}
				sigprop.advance(cnt)

				if n == 4 {
					t := newSig >> 28
					t |= ((t & 0xE) >> 1) | ((t & 7) << 1)
					curMbr[k+1] |= t & ^curSig[k+1]
				}
			}
		}
		newSig |= curSig[k]
		ux := (newSig & 0x88888888) >> 3
		tx := ux | (ux << 4) | (ux >> 4)
		if i > 0 {
			nxtMbr[k-1] |= (ux << 28) & ^nxtSig[k-1]
		}
		nxtMbr[k] |= tx & ^nxtSig[k]
		nxtMbr[k+1] |= (ux >> 28) & ^nxtSig[k+1]
		k++
	}
}
