package tcd

// Encode-side port of tcd.c (owned by W9). Mirrors the C control flow of
// opj_tcd_encode_tile and its pipeline: DC level shift, MCT, DWT, tier-1,
// rate allocation and tier-2, plus the tile-data ingest helpers.
//
// The library never panics: all failures return an error. Float math order is
// preserved verbatim (rate allocation uses float64 accumulators exactly as C).

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/dwt"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/mct"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/t1"
	"github.com/mgilbir/gopenjpeg/internal/t2"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

const t1NmsedecFracbits = 6 // T1_NMSEDEC_FRACBITS = T1_NMSEDEC_BITS(7) - 1

// GetEncoderInputBufferSize ports opj_tcd_get_encoder_input_buffer_size: the
// number of bytes the caller-provided (raw) tile sample buffer must contain.
func (t *TCD) GetEncoderInputBufferSize() uint64 {
	var dataSize uint64
	tl := t.tile()
	for i := uint32(0); i < t.Image.Numcomps; i++ {
		imgComp := &t.Image.Comps[i]
		tilec := &tl.Comps[i]
		sizeComp := imgComp.Prec >> 3
		if imgComp.Prec&7 != 0 {
			sizeComp++
		}
		if sizeComp == 3 {
			sizeComp = 4
		}
		dataSize += uint64(sizeComp) *
			(uint64(tilec.X1-tilec.X0) * uint64(tilec.Y1-tilec.Y0))
	}
	return dataSize
}

// AllocTileComponentData ports the opj_alloc_tile_component_data loop in
// opj_j2k_encode: allocate each tile-component's coefficient buffer.
func (t *TCD) AllocTileComponentData() bool {
	tl := t.tile()
	for i := range tl.Comps {
		if !allocTileComponentData(&tl.Comps[i]) {
			return false
		}
	}
	return true
}

// GetTileData ports opj_j2k_get_tile_data (+ opj_get_tile_dimensions): pack the
// image samples for the current tile into dst as a contiguous, all-component,
// zero-offset buffer using the per-component byte size.
func (t *TCD) GetTileData(dst []byte) {
	pos := 0
	tl := t.tile()
	for i := uint32(0); i < t.Image.Numcomps; i++ {
		img := t.Image
		imgComp := &img.Comps[i]
		tilec := &tl.Comps[i]

		sizeComp := imgComp.Prec >> 3
		if imgComp.Prec&7 != 0 {
			sizeComp++
		}
		if sizeComp == 3 {
			sizeComp = 4
		}
		width := uint32(tilec.X1 - tilec.X0)
		height := uint32(tilec.Y1 - tilec.Y0)
		offsetX := opjmath.UintCeildiv(img.X0, imgComp.Dx)
		offsetY := opjmath.UintCeildiv(img.Y0, imgComp.Dy)
		imageWidth := opjmath.UintCeildiv(img.X1-img.X0, imgComp.Dx)
		stride := imageWidth - width
		tileOffset := (uint32(tilec.X0) - offsetX) + (uint32(tilec.Y0)-offsetY)*imageWidth

		src := imgComp.Data
		si := int(tileOffset)
		switch sizeComp {
		case 1:
			if imgComp.Sgnd != 0 {
				for j := uint32(0); j < height; j++ {
					for k := uint32(0); k < width; k++ {
						dst[pos] = byte(int8(src[si]))
						si++
						pos++
					}
					si += int(stride)
				}
			} else {
				for j := uint32(0); j < height; j++ {
					for k := uint32(0); k < width; k++ {
						dst[pos] = byte(src[si] & 0xff)
						si++
						pos++
					}
					si += int(stride)
				}
			}
		case 2:
			for j := uint32(0); j < height; j++ {
				for k := uint32(0); k < width; k++ {
					v := uint16(src[si])
					dst[pos] = byte(v)
					dst[pos+1] = byte(v >> 8)
					si++
					pos += 2
				}
				si += int(stride)
			}
		case 4:
			for j := uint32(0); j < height; j++ {
				for k := uint32(0); k < width; k++ {
					v := uint32(src[si])
					dst[pos] = byte(v)
					dst[pos+1] = byte(v >> 8)
					dst[pos+2] = byte(v >> 16)
					dst[pos+3] = byte(v >> 24)
					si++
					pos += 4
				}
				si += int(stride)
			}
		}
	}
}

// CopyTileData ports opj_tcd_copy_tile_data: unpack the raw interleaved-by-
// component sample bytes in src into the tile-component int32 buffers.
func (t *TCD) CopyTileData(src []byte) error {
	dataSize := t.GetEncoderInputBufferSize()
	if dataSize != uint64(len(src)) {
		return errTileGeometry
	}
	tl := t.tile()
	pos := 0
	for i := uint32(0); i < t.Image.Numcomps; i++ {
		imgComp := &t.Image.Comps[i]
		tilec := &tl.Comps[i]
		sizeComp := imgComp.Prec >> 3
		remaining := imgComp.Prec & 7
		nbElem := int(tilec.X1-tilec.X0) * int(tilec.Y1-tilec.Y0)
		if remaining != 0 {
			sizeComp++
		}
		if sizeComp == 3 {
			sizeComp = 4
		}
		dst := tilec.Data
		switch sizeComp {
		case 1:
			if imgComp.Sgnd != 0 {
				for j := 0; j < nbElem; j++ {
					dst[j] = int32(int8(src[pos]))
					pos++
				}
			} else {
				for j := 0; j < nbElem; j++ {
					dst[j] = int32(src[pos]) & 0xff
					pos++
				}
			}
		case 2:
			if imgComp.Sgnd != 0 {
				for j := 0; j < nbElem; j++ {
					v := int16(uint16(src[pos]) | uint16(src[pos+1])<<8)
					dst[j] = int32(v)
					pos += 2
				}
			} else {
				for j := 0; j < nbElem; j++ {
					v := uint16(src[pos]) | uint16(src[pos+1])<<8
					dst[j] = int32(v) & 0xffff
					pos += 2
				}
			}
		case 4:
			for j := 0; j < nbElem; j++ {
				v := uint32(src[pos]) | uint32(src[pos+1])<<8 |
					uint32(src[pos+2])<<16 | uint32(src[pos+3])<<24
				dst[j] = int32(v)
				pos += 4
			}
		}
	}
	return nil
}

// EncodeTile ports opj_tcd_encode_tile. On the first tile-part (cur_tp_num==0)
// it runs the full DC-shift/MCT/DWT/T1/rate-allocation pipeline, then tier-2
// writes the packets of the current tile-part into dest. Returns the number of
// bytes written.
func (t *TCD) EncodeTile(tileNo uint32, dest []byte, maxLength uint32,
	markerInfo *tile.MarkerInfo, mgr *event.Manager) (uint32, error) {

	if t.CurTpNum == 0 {
		t.TcdTileno = tileNo
		t.TCP = &t.CP.Tcps[tileNo]

		if err := t.dcLevelShiftEncode(); err != nil {
			return 0, err
		}
		if err := t.mctEncode(); err != nil {
			return 0, err
		}
		if err := t.dwtEncode(); err != nil {
			return 0, err
		}
		if err := t.t1Encode(); err != nil {
			return 0, err
		}
		if err := t.rateAllocateEncode(dest, maxLength, mgr); err != nil {
			return 0, err
		}
	}

	// opj_j2k_write_sod resets the tile packet counter when the first
	// tile-part (cur_tp_num==0) is emitted.
	if t.CurTpNum == 0 {
		t.tile().Packno = 0
	}

	return t.t2Encode(dest, maxLength, markerInfo, mgr)
}

// dcLevelShiftEncode ports opj_tcd_dc_level_shift_encode.
func (t *TCD) dcLevelShiftEncode() error {
	tl := t.tile()
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		tilec := &tl.Comps[compno]
		tccp := &t.TCP.TCCPs[compno]
		nbElem := int(tilec.X1-tilec.X0) * int(tilec.Y1-tilec.Y0)
		cur := tilec.Data
		shift := tccp.MDcLevelShift
		if tccp.Qmfbid == 1 {
			for i := 0; i < nbElem; i++ {
				cur[i] -= shift
			}
		} else {
			for i := 0; i < nbElem; i++ {
				f := float32(cur[i] - shift)
				cur[i] = int32(math.Float32bits(f))
			}
		}
	}
	return nil
}

// mctEncode ports opj_tcd_mct_encode.
func (t *TCD) mctEncode() error {
	tl := t.tile()
	tcp := t.TCP
	tc0 := &tl.Comps[0]
	samples := int(tc0.X1-tc0.X0) * int(tc0.Y1-tc0.Y0)

	if tcp.MCT == 0 {
		return nil
	}

	if tcp.MCT == 2 {
		if tcp.MMctCodingMatrix == nil {
			return nil
		}
		data := make([][]int32, tl.Numcomps)
		for i := uint32(0); i < tl.Numcomps; i++ {
			data[i] = tl.Comps[i].Data
		}
		mct.EncodeCustom(tcp.MMctCodingMatrix, samples, data, tl.Numcomps)
		return nil
	}

	if tcp.TCCPs[0].Qmfbid == 0 {
		d0, d1, d2 := tl.Comps[0].Data, tl.Comps[1].Data, tl.Comps[2].Data
		c0, c1, c2 := bitsToFloat(d0, samples), bitsToFloat(d1, samples), bitsToFloat(d2, samples)
		mct.EncodeReal(c0, c1, c2, samples)
		floatToBits(c0, d0, samples)
		floatToBits(c1, d1, samples)
		floatToBits(c2, d2, samples)
	} else {
		mct.Encode(tl.Comps[0].Data, tl.Comps[1].Data, tl.Comps[2].Data, samples)
	}
	return nil
}

// dwtEncode ports opj_tcd_dwt_encode.
func (t *TCD) dwtEncode() error {
	tl := t.tile()
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		tilec := &tl.Comps[compno]
		tccp := &t.TCP.TCCPs[compno]
		dc := t.mapTileCompToDWT(tilec)
		var ok bool
		if tccp.Qmfbid == 1 {
			ok = dwt.Encode(dc)
		} else if tccp.Qmfbid == 0 {
			ok = dwt.EncodeReal(dc)
		} else {
			ok = true
		}
		if !ok {
			return errTierDecode
		}
		// dwt.Encode/EncodeReal transform tilec.Data in place (dc.Data aliases it).
	}
	return nil
}

// t1Encode ports opj_tcd_t1_encode.
func (t *TCD) t1Encode() error {
	tcp := t.TCP
	var mctNorms []float64
	var mctNumcomps uint32
	if tcp.MCT == 1 {
		mctNumcomps = 3
		if tcp.TCCPs[0].Qmfbid == 0 {
			n := mct.GetMctNormsReal()
			mctNorms = n[:]
		} else {
			n := mct.GetMctNorms()
			mctNorms = n[:]
		}
	} else {
		mctNumcomps = t.Image.Numcomps
		mctNorms = tcp.MctNorms
	}
	return t.encodeCblks(mctNorms, mctNumcomps)
}

// encCblkJob is one per-code-block tier-1 encode unit, mirroring the C
// opj_t1_cblk_encode_processor job. Jobs are enumerated in the canonical
// component/resolution/band/precinct/code-block order so distortion can be
// re-summed deterministically regardless of worker scheduling.
type encCblkJob struct {
	cblk          *tile.CblkEnc
	band          *tile.Band
	tilec         *tile.TileComp
	tccp          *cparams.TCCP
	resno, compno uint32
}

// encodeCblks ports opj_t1_encode_cblks. Each code-block is coded independently
// into its own CblkEnc, so with NumThreads>1 the jobs fan out across N workers
// (each with a private t1.T1 encode state) exactly like the C thread pool.
//
// Determinism / byte-identity: the C reference sums per-code-block distortion
// into tile->distotile under a mutex in worker-completion order, so its float
// summation order varies with scheduling; empirically opj_compress output is
// nonetheless byte-stable across -threads because the tiny float differences do
// not cross the discrete rate-allocation truncation boundaries. Rather than
// depend on that, this port accumulates each job's distortion into a slot and
// sums the slots in canonical code-block order afterwards, so tile.Distotile —
// and therefore rate allocation and the output bytes — is bit-identical to the
// sequential encode (and thus to C -threads 1) for every worker count.
func (t *TCD) encodeCblks(mctNorms []float64, mctNumcomps uint32) error {
	tl := t.tile()
	tl.Distotile = 0

	var jobs []encCblkJob
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		tilec := &tl.Comps[compno]
		tccp := &t.TCP.TCCPs[compno]
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
						jobs = append(jobs, encCblkJob{
							cblk:   &prc.CblksEnc[cblkno],
							band:   band,
							tilec:  tilec,
							tccp:   tccp,
							resno:  resno,
							compno: compno,
						})
					}
				}
			}
		}
	}

	dist := make([]float64, len(jobs))
	n := t.NumThreads
	if n < 1 {
		n = 1
	}
	if n > len(jobs) {
		n = len(jobs)
	}
	if n <= 1 {
		state := t1.New(true)
		for i := range jobs {
			j := &jobs[i]
			dist[i] = t.cblkEncodeProcessor(state, j.cblk, j.band, j.tilec, j.tccp,
				j.resno, j.compno, mctNorms, mctNumcomps)
		}
	} else {
		var (
			next int64
			wg   sync.WaitGroup
		)
		for k := 0; k < n; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				state := t1.New(true)
				for {
					idx := int(atomic.AddInt64(&next, 1)) - 1
					if idx >= len(jobs) {
						return
					}
					j := &jobs[idx]
					dist[idx] = t.cblkEncodeProcessor(state, j.cblk, j.band, j.tilec, j.tccp,
						j.resno, j.compno, mctNorms, mctNumcomps)
				}
			}()
		}
		wg.Wait()
	}

	// Sum in canonical code-block order for a scheduling-independent result.
	for i := range dist {
		tl.Distotile += dist[i]
	}
	return nil
}

// cblkEncodeProcessor ports opj_t1_cblk_encode_processor: fill t1->data from
// the tile buffer in "zigzag" (column-of-4) order with the fixed-point shift,
// run the code-block encoder, and copy the result back into the tile CblkEnc.
func (t *TCD) cblkEncodeProcessor(state *t1.T1, cblk *tile.CblkEnc, band *tile.Band,
	tilec *tile.TileComp, tccp *cparams.TCCP, resno, compno uint32,
	mctNorms []float64, mctNumcomps uint32) float64 {

	tileW := uint32(tilec.X1 - tilec.X0)
	x := cblk.X0 - band.X0
	y := cblk.Y0 - band.Y0
	if band.Bandno&1 != 0 {
		pres := &tilec.Resolutions[resno-1]
		x += pres.X1 - pres.X0
	}
	if band.Bandno&2 != 0 {
		pres := &tilec.Resolutions[resno-1]
		y += pres.Y1 - pres.Y0
	}

	cblkW := uint32(cblk.X1 - cblk.X0)
	cblkH := uint32(cblk.Y1 - cblk.Y0)
	base := int(y)*int(tileW) + int(x)
	tdata := tilec.Data

	t1data := make([]int32, int(cblkW)*int(cblkH))
	di := 0

	jEnd := cblkH & ^uint32(3)
	if tccp.Qmfbid == 1 {
		for j := uint32(0); j < jEnd; j += 4 {
			for i := uint32(0); i < cblkW; i++ {
				t1data[di+0] = int32(uint32(tdata[base+int((j+0)*tileW+i)]) << t1NmsedecFracbits)
				t1data[di+1] = int32(uint32(tdata[base+int((j+1)*tileW+i)]) << t1NmsedecFracbits)
				t1data[di+2] = int32(uint32(tdata[base+int((j+2)*tileW+i)]) << t1NmsedecFracbits)
				t1data[di+3] = int32(uint32(tdata[base+int((j+3)*tileW+i)]) << t1NmsedecFracbits)
				di += 4
			}
		}
		if jEnd < cblkH {
			for i := uint32(0); i < cblkW; i++ {
				for k := jEnd; k < cblkH; k++ {
					t1data[di] = int32(uint32(tdata[base+int(k*tileW+i)]) << t1NmsedecFracbits)
					di++
				}
			}
		}
	} else {
		stepsize := band.Stepsize
		mul := float32(int32(1) << t1NmsedecFracbits)
		// The C source computes (f / stepsize) * (1<<T1_NMSEDEC_FRACBITS), but the
		// stock libopenjp2 that opj_compress links is built -ffast-math, and gcc's
		// -freassoc pass hoists the constant division: it emits
		// quantized = lrintf(f * (mul / stepsize)), computing mul/stepsize once per
		// band as a float32 loop invariant (verified in the shipped binary's
		// opj_t1_cblk_encode_processor: `movaps xmm0,<64.0f>; divss xmm0,stepsize`
		// then a per-sample `mulss`). f*(mul/stepsize) rounds differently from
		// (f/stepsize)*mul by up to 1 ULP, which flips ~1/1000 of quantized
		// coefficients by 1 LSB. That is invisible when layers truncate the low
		// bit-planes (the 8-bit rate-limited encode-gate cells), but breaks
		// byte-identity on all-pass irreversible streams (cinema/IMF, or any -r<=1).
		// Mirror the binary so those streams are byte-identical with opj_compress.
		invStep := mul / stepsize
		conv := func(idx int) int32 {
			f := math.Float32frombits(uint32(tdata[base+idx]))
			return int32(lrintf(f * invStep))
		}
		for j := uint32(0); j < jEnd; j += 4 {
			for i := uint32(0); i < cblkW; i++ {
				t1data[di+0] = conv(int((j+0)*tileW + i))
				t1data[di+1] = conv(int((j+1)*tileW + i))
				t1data[di+2] = conv(int((j+2)*tileW + i))
				t1data[di+3] = conv(int((j+3)*tileW + i))
				di += 4
			}
		}
		if jEnd < cblkH {
			for i := uint32(0); i < cblkW; i++ {
				for k := jEnd; k < cblkH; k++ {
					t1data[di] = conv(int(k*tileW + i))
					di++
				}
			}
		}
	}

	state.SetData(t1data, cblkW, cblkH)

	t1cblk := &t1.CodeBlockEnc{
		X0: cblk.X0, Y0: cblk.Y0, X1: cblk.X1, Y1: cblk.Y1,
	}
	level := tilec.Numresolutions - 1 - resno
	cum := state.EncodeCblk(t1cblk, band.Bandno, compno, level, tccp.Qmfbid,
		float64(band.Stepsize), tccp.Cblksty, t.tile().Numcomps, mctNorms, mctNumcomps)

	// Copy the encode result back into the tile-owned CblkEnc.
	cblk.Numbps = t1cblk.Numbps
	cblk.Totalpasses = t1cblk.Totalpasses
	cblk.Data = append(cblk.Data[:0], t1cblk.Data...)
	if uint32(len(cblk.Passes)) < t1cblk.Totalpasses {
		cblk.Passes = make([]tile.Pass, t1cblk.Totalpasses)
	}
	for p := uint32(0); p < t1cblk.Totalpasses; p++ {
		sp := &t1cblk.Passes[p]
		cblk.Passes[p] = tile.Pass{
			Rate:          sp.Rate,
			Distortiondec: sp.DistortionDec,
			Len:           sp.Len,
			Term:          sp.Term != 0,
		}
	}
	return cum
}

// t2Encode ports opj_tcd_t2_encode: the tier-2 FINAL_PASS packet writer.
func (t *TCD) t2Encode(dest []byte, maxLength uint32, markerInfo *tile.MarkerInfo,
	mgr *event.Manager) (uint32, error) {

	engine := t2.Create(t.Image, t.CP)
	written, ok := engine.EncodePackets(t.TcdTileno, t.tile(), t.TCP.Numlayers,
		dest, maxLength, nil, markerInfo, t.TpNum, t.TpPos, t.CurPino,
		cparams.FinalPass, mgr)
	if !ok {
		return 0, errTierDecode
	}
	return written, nil
}
