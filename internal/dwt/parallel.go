package dwt

import "sync"

// This file adds the row/column parallelism the C reference applies in
// opj_dwt_decode_tile / opj_dwt_decode_tile_97 (dwt.c): the horizontal pass
// over rows and the vertical pass over columns are each split into contiguous
// chunks submitted to the thread pool, every job owning a private scratch
// buffer (opj_aligned_32_malloc per job). We reproduce that with goroutines.
// Because the rows of the H pass and the columns of the V pass are mutually
// independent (each touches only its own row/column of the coefficient buffer,
// scratch is per-worker), any chunking yields bit-identical output; the H pass
// fully completes before the V pass begins (the wait-for-completion barrier).

// parChunksI32 splits [0,total) into up to n contiguous chunks and runs work on
// each in its own goroutine, giving each a fresh scratch buffer of scratchLen
// int32 words. With n<=1 or total<=1 it runs on the caller goroutine with a
// single scratch allocation, exactly reproducing the sequential path.
func parChunksI32(n, total, scratchLen int, work func(mem []int32, start, end uint32)) {
	if n < 1 {
		n = 1
	}
	if n > total {
		n = total
	}
	if n <= 1 || total <= 1 {
		work(make([]int32, scratchLen), 0, uint32(total))
		return
	}
	step := total / n
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		s := g * step
		e := s + step
		if g == n-1 {
			e = total
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			work(make([]int32, scratchLen), uint32(s), uint32(e))
		}(s, e)
	}
	wg.Wait()
}

// parGroupsF32 splits [0,groups) full v8 groups into up to n contiguous chunks,
// each goroutine getting a fresh float32 scratch buffer of scratchLen words. The
// tail (partial group) is handled by the caller after this returns, so only full
// groups are parallelized. Sequential fallback when n<=1 or groups<=1.
func parGroupsF32(n, groups, scratchLen int, work func(mem []float32, gStart, gEnd uint32)) {
	if groups <= 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	if n > groups {
		n = groups
	}
	if n <= 1 || groups <= 1 {
		work(make([]float32, scratchLen), 0, uint32(groups))
		return
	}
	step := groups / n
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		s := g * step
		e := s + step
		if g == n-1 {
			e = groups
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			work(make([]float32, scratchLen), uint32(s), uint32(e))
		}(s, e)
	}
	wg.Wait()
}
