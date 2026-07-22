package tcd

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/ht"
	"github.com/mgilbir/gopenjpeg/internal/t1"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// t1Job describes one code-block to decode via tier-1. It ports the fields the
// C opj_t1_cblk_decode_processing_job_t carries (opj_thread_pool_submit_job in
// opj_t1_decode_cblks). Each job's output goes to a disjoint region of
// tilec.Data (whole-tile) or the code-block's own DecodedData buffer, so jobs
// are independent and can run in any order with identical results.
type t1Job struct {
	resno uint32
	tilec *tile.TileComp
	tccp  *cparams.TCCP
	band  *tile.Band
	cblk  *tile.CblkDec
}

// t1Worker holds the per-goroutine tier-1 decode state, mirroring the
// per-thread opj_t1_t (and the HTJ2K decoder handle) that the C reference keeps
// in thread-local storage (OPJ_TLS_KEY_T1). Reused across all code-blocks a
// worker processes.
type t1Worker struct {
	t1 *t1.T1
	ht *ht.Decoder
}

// t1Decode ports opj_tcd_t1_decode: decode all code-blocks of the tile via
// tier-1, for every component that must be decoded. The C reference submits one
// job per code-block to the thread pool (opj_t1_decode_cblks); we collect the
// same jobs and run them across NumThreads workers.
func (t *TCD) t1Decode(mgr *event.Manager) error {
	tl := t.tile()
	tcp := t.TCP

	checkPterm := false
	if tcp.NumLayersToDecode == tcp.Numlayers &&
		(tcp.TCCPs[0].Cblksty&cparams.CCPCblkStyPterm) != 0 {
		checkPterm = true
	}

	var jobs []t1Job
	for compno := uint32(0); compno < tl.Numcomps; compno++ {
		if t.UsedComponent != nil && !t.UsedComponent[compno] {
			continue
		}
		jobs = t.collectT1Jobs(jobs, compno)
	}
	return t.runT1Jobs(jobs, checkPterm, mgr)
}

// collectT1Jobs ports the job-submission loop of opj_t1_decode_cblks for one
// tile-component: it walks resolutions/bands/precincts/code-blocks, discards the
// decoded_data of code-blocks outside the area of interest (exactly as C does)
// and appends a job for every code-block that must be decoded.
func (t *TCD) collectT1Jobs(jobs []t1Job, compno uint32) []t1Job {
	tilec := &t.tile().Comps[compno]
	tccp := &t.TCP.TCCPs[compno]

	for resno := uint32(0); resno < tilec.MinimumNumResolutions; resno++ {
		res := &tilec.Resolutions[resno]
		for bandno := uint32(0); bandno < res.Numbands; bandno++ {
			band := &res.Bands[bandno]
			for precno := uint32(0); precno < res.Pw*res.Ph; precno++ {
				prec := &band.Precincts[precno]
				if !t.isSubbandAreaOfInterest(compno, resno, band.Bandno,
					uint32(prec.X0), uint32(prec.Y0), uint32(prec.X1), uint32(prec.Y1)) {
					for cblkno := uint32(0); cblkno < prec.Cw*prec.Ch; cblkno++ {
						prec.CblksDec[cblkno].DecodedData = nil
					}
					continue
				}
				for cblkno := uint32(0); cblkno < prec.Cw*prec.Ch; cblkno++ {
					cblk := &prec.CblksDec[cblkno]
					if !t.isSubbandAreaOfInterest(compno, resno, band.Bandno,
						uint32(cblk.X0), uint32(cblk.Y0), uint32(cblk.X1), uint32(cblk.Y1)) {
						cblk.DecodedData = nil
						continue
					}
					if !t.WholeTileDecoding {
						cblkW := uint32(cblk.X1 - cblk.X0)
						cblkH := uint32(cblk.Y1 - cblk.Y0)
						if cblk.DecodedData != nil {
							continue
						}
						if cblkW == 0 || cblkH == 0 {
							continue
						}
					}
					jobs = append(jobs, t1Job{resno: resno, tilec: tilec, tccp: tccp, band: band, cblk: cblk})
				}
			}
		}
	}
	return jobs
}

// runT1Jobs executes the collected tier-1 jobs. With NumThreads<=1 (the default)
// it runs them sequentially with a single reusable worker, exactly reproducing
// the C single-thread pool. With NumThreads>1 it fans the jobs across N
// goroutines, each with its own worker state; because every job writes to a
// disjoint output region the result is identical to the sequential decode. Error
// semantics are deterministic: the error of the lowest-indexed failing job wins
// (mirroring the C *pret first-failure latch).
func (t *TCD) runT1Jobs(jobs []t1Job, checkPterm bool, mgr *event.Manager) error {
	n := t.NumThreads
	if n < 1 {
		n = 1
	}
	if n > len(jobs) {
		n = len(jobs)
	}
	if n <= 1 {
		w := &t1Worker{t1: t1.New(false)}
		for i := range jobs {
			if err := t.t1DecodeBlock(w, jobs[i], checkPterm, mgr); err != nil {
				return err
			}
		}
		return nil
	}

	var (
		mu          sync.Mutex
		firstErrIdx = len(jobs)
		firstErr    error
		next        int64
	)
	// Guard event-manager access so concurrent warnings/errors don't race the
	// user callback, mirroring C's p_manager_mutex.
	lmgr := lockingManager(mgr, &mu)

	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := &t1Worker{t1: t1.New(false)}
			for {
				idx := int(atomic.AddInt64(&next, 1)) - 1
				if idx >= len(jobs) {
					return
				}
				mu.Lock()
				stop := idx > firstErrIdx
				mu.Unlock()
				if stop {
					return
				}
				if err := t.t1DecodeBlock(w, jobs[idx], checkPterm, lmgr); err != nil {
					mu.Lock()
					if idx < firstErrIdx {
						firstErrIdx = idx
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// lockingManager returns an event.Manager whose handlers acquire mu before
// invoking the underlying handlers, so parallel workers can log safely
// (mirroring opj_mutex around opj_event_msg). Returns nil unchanged.
func lockingManager(m *event.Manager, mu *sync.Mutex) *event.Manager {
	if m == nil {
		return nil
	}
	wrap := func(h event.MsgCallback) event.MsgCallback {
		if h == nil {
			return nil
		}
		return func(msg string) {
			mu.Lock()
			defer mu.Unlock()
			h(msg)
		}
	}
	return &event.Manager{
		ErrorHandler:   wrap(m.ErrorHandler),
		WarningHandler: wrap(m.WarningHandler),
		InfoHandler:    wrap(m.InfoHandler),
	}
}

// t1DecodeBlock ports opj_t1_clbl_decode_processor for a single code-block,
// using the worker's private tier-1/HT state.
func (t *TCD) t1DecodeBlock(w *t1Worker, job t1Job, checkPterm bool, mgr *event.Manager) error {
	resno, tilec, tccp, band, cblk := job.resno, job.tilec, job.tccp, job.band, job.cblk
	state := w.t1

	if !t.WholeTileDecoding {
		cblkW := uint32(cblk.X1 - cblk.X0)
		cblkH := uint32(cblk.Y1 - cblk.Y0)
		cblk.DecodedData = make([]int32, cblkW*cblkH)
	} else if cblk.DecodedData != nil {
		cblk.DecodedData = nil
	}

	tileW := uint32(tilec.Resolutions[tilec.MinimumNumResolutions-1].X1 -
		tilec.Resolutions[tilec.MinimumNumResolutions-1].X0)

	t1cblk := mapCblkDec(cblk)

	roishift := uint32(tccp.Roishift)
	// srcData/srcW/srcH mirror the C convention of reading the decoder
	// working buffer when the code-block has no decoded_data of its own.
	var srcData []int32
	var srcW, srcH uint32
	if (tccp.Cblksty & cparams.CCPCblkStyHT) != 0 {
		// Port of the opj_t1_ht_decode_cblk dispatch in t1.c; mb is
		// band->numbps, set on the cblk by t2 in the C reference.
		if w.ht == nil {
			w.ht = ht.New()
		}
		if ok, err := w.ht.DecodeCblk(t1cblk, band.Bandno, roishift, tccp.Cblksty, uint32(band.Numbps), mgr); err != nil || !ok {
			if err != nil {
				return err
			}
			return errTierDecode
		}
		srcData, srcW, srcH = w.ht.Data(), w.ht.W(), w.ht.H()
	} else {
		if ok, err := state.DecodeCblk(t1cblk, band.Bandno, roishift, tccp.Cblksty, checkPterm); err != nil || !ok {
			if err != nil {
				return err
			}
			return errTierDecode
		}
		srcData, srcW, srcH = state.Data(), state.W(), state.H()
	}
	// Propagate any decoded_data buffer the tier-1 decoder populated.
	cblk.DecodedData = t1cblk.DecodedData

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

	var datap []int32
	if cblk.DecodedData != nil {
		datap = cblk.DecodedData
	} else {
		datap = srcData
	}
	cblkW := srcW
	cblkH := srcH

	// ROI de-scaling.
	if tccp.Roishift != 0 {
		t1.RoiShift(datap, cblkW, cblkH, roishift)
	}

	if cblk.DecodedData != nil {
		t1.Dequantize(datap, cblkW, cblkH, tccp.Qmfbid, band.Stepsize)
		return nil
	}

	// Whole-tile: place directly into tilec.Data at (x,y) with stride tileW.
	if tccp.Qmfbid == 1 {
		base := int(y)*int(tileW) + int(x)
		for j := uint32(0); j < cblkH; j++ {
			row := base + int(j)*int(tileW)
			src := int(j) * int(cblkW)
			for i := uint32(0); i < cblkW; i++ {
				tilec.Data[row+int(i)] = datap[src+int(i)] / 2
			}
		}
	} else {
		stepsize := 0.5 * band.Stepsize
		base := int(y)*int(tileW) + int(x)
		si := 0
		for j := uint32(0); j < cblkH; j++ {
			row := base + int(j)*int(tileW)
			for i := uint32(0); i < cblkW; i++ {
				tmp := float32(datap[si]) * stepsize
				tilec.Data[row+int(i)] = int32(math.Float32bits(tmp))
				si++
			}
		}
	}
	return nil
}

// mapCblkDec maps a tile.CblkDec (tcd's data model) to the t1.CodeBlockDec type
// that package t1 consumes, aliasing the chunk/segment/decoded backing slices.
func mapCblkDec(cblk *tile.CblkDec) *t1.CodeBlockDec {
	out := &t1.CodeBlockDec{
		X0:          cblk.X0,
		Y0:          cblk.Y0,
		X1:          cblk.X1,
		Y1:          cblk.Y1,
		Numbps:      cblk.Numbps,
		NumChunks:   cblk.Numchunks,
		RealNumSegs: cblk.RealNumSegs,
		Corrupted:   cblk.Corrupted,
		DecodedData: cblk.DecodedData,
	}
	if cblk.Numchunks > 0 {
		out.Chunks = make([]t1.Chunk, cblk.Numchunks)
		for i := uint32(0); i < cblk.Numchunks; i++ {
			out.Chunks[i] = t1.Chunk{Data: cblk.Chunks[i].Data, Len: cblk.Chunks[i].Len}
		}
	}
	if cblk.RealNumSegs > 0 {
		out.Segs = make([]t1.Seg, cblk.RealNumSegs)
		for i := uint32(0); i < cblk.RealNumSegs; i++ {
			out.Segs[i] = t1.Seg{Len: cblk.Segs[i].Len, RealNumPasses: cblk.Segs[i].RealNumPasses}
		}
	}
	return out
}
