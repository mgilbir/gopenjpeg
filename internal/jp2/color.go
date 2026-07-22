package jp2

import (
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// checkColor ports opj_jp2_check_color: validate the cdef and pclr/cmap tables
// against the image before they are applied. Every check here is a hardened
// security path (guarding against out-of-range channel indices, duplicate or
// incomplete channel definitions and malformed component maps); all are ported
// verbatim. It returns false when the colour metadata is unusable.
func checkColor(img *image.Image, color *Color, mgr *event.Manager) bool {
	// testcase 4149.pdf.SIGSEGV.cf7.3501
	if color.Cdef != nil {
		info := color.Cdef.Info
		n := color.Cdef.N
		nrChannels := img.Numcomps

		// cdef applies to cmap channels if any
		if color.Pclr != nil && color.Pclr.Cmap != nil {
			nrChannels = uint32(color.Pclr.NrChannels)
		}

		for i := uint16(0); i < n; i++ {
			if uint32(info[i].Cn) >= nrChannels {
				mgr.Errorf("Invalid component index %d (>= %d).\n", info[i].Cn, nrChannels)
				return false
			}
			if info[i].Asoc == 65535 {
				continue
			}
			if info[i].Asoc > 0 && uint32(info[i].Asoc-1) >= nrChannels {
				mgr.Errorf("Invalid component index %d (>= %d).\n", info[i].Asoc-1, nrChannels)
				return false
			}
		}

		// issue 397: ISO 15444-1 requires a complete list of channel definitions.
		for nrChannels > 0 {
			var i uint16
			for i = 0; i < n; i++ {
				if uint32(info[i].Cn) == nrChannels-1 {
					break
				}
			}
			if i == n {
				mgr.Errorf("Incomplete channel definitions.\n")
				return false
			}
			nrChannels--
		}
	}

	// testcases 451.pdf.SIGSEGV.f4c.3723, 451.pdf.SIGSEGV.5b5.3723 and
	// 66ea31acbb0f23a2bbc91f64d69a03f5_signal_sigsegv_13937c0_7030_5725.pdf
	if color.Pclr != nil && color.Pclr.Cmap != nil {
		nrChannels := color.Pclr.NrChannels
		cmap := color.Pclr.Cmap
		isSane := true

		// verify that all original components match an existing one
		for i := 0; i < int(nrChannels); i++ {
			if uint32(cmap[i].Cmp) >= img.Numcomps {
				mgr.Errorf("Invalid component index %d (>= %d).\n", cmap[i].Cmp, img.Numcomps)
				isSane = false
			}
		}

		pcolUsage := make([]bool, nrChannels)
		// verify that no component is targeted more than once
		for i := 0; i < int(nrChannels); i++ {
			mtyp := cmap[i].Mtyp
			pcol := cmap[i].Pcol
			// See ISO 15444-1 Table I.14 – MTYPi field values
			switch {
			case mtyp != 0 && mtyp != 1:
				mgr.Errorf("Invalid value for cmap[%d].mtyp = %d.\n", i, mtyp)
				isSane = false
			case pcol >= nrChannels:
				mgr.Errorf("Invalid component/palette index for direct mapping %d.\n", pcol)
				isSane = false
			case pcolUsage[pcol] && mtyp == 1:
				mgr.Errorf("Component %d is mapped twice.\n", pcol)
				isSane = false
			case mtyp == 0 && pcol != 0:
				// I.5.3.5 PCOL: if MTYP is 0, PCOL shall be 0.
				mgr.Errorf("Direct use at #%d however pcol=%d.\n", i, pcol)
				isSane = false
			case mtyp == 1 && int(pcol) != i:
				// OpenJPEG implementation limitation (see the assert(i == pcol)
				// in applyPclr).
				mgr.Errorf("Implementation limitation: for palette mapping, "+
					"pcol[%d] should be equal to %d, but is equal to %d.\n", i, i, pcol)
				isSane = false
			default:
				pcolUsage[pcol] = true
			}
		}
		// verify that all components are targeted at least once
		for i := 0; i < int(nrChannels); i++ {
			if !pcolUsage[i] && cmap[i].Mtyp != 0 {
				mgr.Errorf("Component %d doesn't have a mapping.\n", i)
				isSane = false
			}
		}
		// Issue 235/447 weird cmap
		if isSane && img.Numcomps == 1 {
			for i := 0; i < int(nrChannels); i++ {
				if !pcolUsage[i] {
					isSane = false
					mgr.Warnf("Component mapping seems wrong. Trying to correct.\n")
					break
				}
			}
			if !isSane {
				isSane = true
				for i := 0; i < int(nrChannels); i++ {
					cmap[i].Mtyp = 1
					cmap[i].Pcol = byte(i)
				}
			}
		}
		if !isSane {
			return false
		}
	}

	return true
}

// applyPclr ports opj_jp2_apply_pclr: expand the palette, replacing the image's
// components with the palette-mapped channels. It handles direct-use channels,
// palette-mapped channels, per-channel precision/sign from the palette, and the
// signed-index clamping ([0, top_k]).
func applyPclr(img *image.Image, color *Color, mgr *event.Manager) bool {
	channelSize := color.Pclr.ChannelSize
	channelSign := color.Pclr.ChannelSign
	entries := color.Pclr.Entries
	cmap := color.Pclr.Cmap
	nrChannels := color.Pclr.NrChannels

	for i := 0; i < int(nrChannels); i++ {
		// Palette mapping: the source component must have data.
		cmp := cmap[i].Cmp
		if img.Comps[cmp].Data == nil {
			mgr.Errorf("image->comps[%d].data == NULL in opj_jp2_apply_pclr().\n", i)
			return false
		}
	}

	oldComps := img.Comps
	newComps := make([]image.Comp, nrChannels)

	for i := 0; i < int(nrChannels); i++ {
		pcol := cmap[i].Pcol
		cmp := cmap[i].Cmp

		// Direct use vs palette mapping (C asserts i==pcol for the mapped case;
		// checkColor has already enforced it).
		if cmap[i].Mtyp == 0 {
			newComps[i] = oldComps[cmp]
		} else {
			newComps[pcol] = oldComps[cmp]
		}

		// Allocate fresh data for the mapped channel.
		newComps[i].Data = make([]int32, uint64(oldComps[cmp].W)*uint64(oldComps[cmp].H))
		newComps[i].Prec = uint32(channelSize[i])
		newComps[i].Sgnd = uint32(channelSign[i])
	}

	topK := int32(color.Pclr.NrEntries) - 1

	for i := 0; i < int(nrChannels); i++ {
		cmp := cmap[i].Cmp
		pcol := cmap[i].Pcol
		src := oldComps[cmp].Data
		maxv := newComps[i].W * newComps[i].H

		if cmap[i].Mtyp == 0 {
			dst := newComps[i].Data
			for j := uint32(0); j < maxv; j++ {
				dst[j] = src[j]
			}
		} else {
			dst := newComps[pcol].Data
			for j := uint32(0); j < maxv; j++ {
				// The index, clamped into [0, top_k].
				k := src[j]
				if k < 0 {
					k = 0
				} else if k > topK {
					k = topK
				}
				// The colour.
				dst[j] = int32(entries[k*int32(nrChannels)+int32(pcol)])
			}
		}
	}

	img.Comps = newComps
	img.Numcomps = uint32(nrChannels)
	return true
}

// applyCdef ports opj_jp2_apply_cdef: reorder colour channels to their
// association order and tag alpha/opacity channels. Out-of-range indices are
// warnings (not errors), matching C.
func applyCdef(img *image.Image, color *Color, mgr *event.Manager) {
	info := color.Cdef.Info
	n := color.Cdef.N

	for i := uint16(0); i < n; i++ {
		// WATCH: acn = asoc - 1 !
		asoc := info[i].Asoc
		cn := info[i].Cn

		if uint32(cn) >= img.Numcomps {
			mgr.Warnf("opj_jp2_apply_cdef: cn=%d, numcomps=%d\n", cn, img.Numcomps)
			continue
		}
		if asoc == 0 || asoc == 65535 {
			img.Comps[cn].Alpha = info[i].Typ
			continue
		}

		acn := asoc - 1
		if uint32(acn) >= img.Numcomps {
			mgr.Warnf("opj_jp2_apply_cdef: acn=%d, numcomps=%d\n", acn, img.Numcomps)
			continue
		}

		// Swap only if colour channel.
		if cn != acn && info[i].Typ == 0 {
			saved := img.Comps[cn]
			img.Comps[cn] = img.Comps[acn]
			img.Comps[acn] = saved

			// Swap channels in following channel definitions; entries j <= i are
			// already processed.
			for j := i + 1; j < n; j++ {
				if info[j].Cn == cn {
					info[j].Cn = acn
				} else if info[j].Cn == acn {
					info[j].Cn = cn
				}
				// asoc is related to colour index. Do not update.
			}
		}

		img.Comps[cn].Alpha = info[i].Typ
	}

	color.Cdef = nil
}

// applyColorPostprocessing ports opj_jp2_apply_color_postprocessing: run
// checkColor and then apply the pclr and cdef rules, in that order, unless the
// caller requested that they be ignored or the codec decoded a component subset
// (in which case all JP2 component transforms are bypassed, exactly as C does).
func (jp2 *JP2) applyColorPostprocessing(img *image.Image, mgr *event.Manager) bool {
	if jp2.codec.NumCompsToDecode() != 0 {
		// Bypass all JP2 component transforms.
		return true
	}

	if !jp2.ignorePclrCmapCdef {
		if !checkColor(img, &jp2.color, mgr) {
			return false
		}

		if jp2.color.Pclr != nil {
			// Part 1, I.5.3.4: either both pclr and cmap, or neither.
			if jp2.color.Pclr.Cmap == nil {
				jp2.color.freePclr()
			} else {
				if !applyPclr(img, &jp2.color, mgr) {
					return false
				}
			}
		}

		// Apply the channel definitions if present.
		if jp2.color.Cdef != nil {
			applyCdef(img, &jp2.color, mgr)
		}
	}

	return true
}
