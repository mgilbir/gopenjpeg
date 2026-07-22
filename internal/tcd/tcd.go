// Package tcd is a pure-Go port of OpenJPEG's tile coder/decoder (tcd.c/tcd.h):
// the stage that sits between the codestream marker machinery (package j2k) and
// the coding engine (t1/t2/dwt/mct). It owns the per-tile geometry allocation
// (opj_tcd_init_decode_tile), and drives the decode pipeline
// (opj_tcd_decode_tile): tier-2 packet decode, tier-1 code-block decode, inverse
// DWT, inverse MCT and DC level-shift, then extraction of decoded samples.
//
// Only the decode path is ported here (W7). The encode-side entry points are
// present as clearly-marked stubs that return errors; W9 ports them.
//
// The library never panics: every failure returns an error. All the bounds and
// overflow guards of the C reference are preserved, since this is a primary CVE
// surface.
package tcd

import (
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// Approximate C struct sizes, used only to preserve the overflow guards of
// opj_tcd_init_tile that reject absurd code-block counts before allocation.
const (
	sizeofCblkDec = 88
	sizeofCblkEnc = 88
)

// TCD ports opj_tcd_t: the tile coder/decoder state.
type TCD struct {
	TpPos       int32  // tp_pos
	TpNum       uint32 // tp_num
	CurTpNum    uint32 // cur_tp_num
	CurTotnumTp uint32 // cur_totnum_tp
	CurPino     uint32 // cur_pino

	TcdImage *tile.Image  // tcd_image
	Image    *image.Image // image
	CP       *cparams.CP  // cp
	TCP      *cparams.TCP // tcp

	TcdTileno uint32 // tcd_tileno
	IsDecoder bool   // m_is_decoder

	WinX0 uint32 // win_x0
	WinY0 uint32 // win_y0
	WinX1 uint32 // win_x1
	WinY1 uint32 // win_y1

	// WholeTileDecoding ports whole_tile_decoding.
	WholeTileDecoding bool
	// UsedComponent ports used_component: nil if all components are decoded,
	// otherwise len == image.Numcomps with true for components to decode.
	UsedComponent []bool

	// NumThreads is the worker count for the parallelizable stages (tier-1
	// code-block decode, inverse DWT). It ports the effect of
	// opj_thread_pool_get_thread_count(tcd->thread_pool): 1 (the default)
	// reproduces the C single-threaded path exactly; N>1 fans code-blocks and
	// DWT row/column chunks across N goroutines, each with private scratch
	// state, writing to disjoint output regions so the result is bit-identical
	// to the sequential decode regardless of scheduling.
	NumThreads int
}

// SetNumThreads sets the worker count for parallel decode stages. n<=1 keeps
// the fully sequential behaviour (the default, matching C's default of a
// single-thread pool).
func (t *TCD) SetNumThreads(n int) { t.NumThreads = n }

// Create ports opj_tcd_create.
func Create(isDecoder bool) *TCD {
	return &TCD{
		IsDecoder: isDecoder,
		TcdImage:  &tile.Image{},
	}
}

// Init ports opj_tcd_init: binds the tcd to an image and coding parameters and
// allocates the (single) working tile with its component array.
func (t *TCD) Init(img *image.Image, cp *cparams.CP) bool {
	t.Image = img
	t.CP = cp
	t.TcdImage.Tiles = make([]tile.Tile, 1)
	tl := &t.TcdImage.Tiles[0]
	tl.Comps = make([]tile.TileComp, img.Numcomps)
	tl.Numcomps = img.Numcomps
	t.TpPos = cp.MEnc.MTpPos
	return true
}

// tile returns the single working tile.
func (t *TCD) tile() *tile.Tile { return &t.TcdImage.Tiles[0] }

// allocTileComponentData ports opj_alloc_tile_component_data. DataSizeNeeded and
// DataSize are byte counts (as in C); the []int32 buffer holds DataSize/4 words.
func allocTileComponentData(tilec *tile.TileComp) bool {
	if tilec.Data == nil ||
		(tilec.DataSizeNeeded > tilec.DataSize && !tilec.OwnsData) {
		tilec.Data = make([]int32, tilec.DataSizeNeeded/4)
		tilec.DataSize = tilec.DataSizeNeeded
		tilec.OwnsData = true
	} else if tilec.DataSizeNeeded > tilec.DataSize {
		tilec.Data = make([]int32, tilec.DataSizeNeeded/4)
		tilec.DataSize = tilec.DataSizeNeeded
		tilec.OwnsData = true
	}
	return true
}

// InitDecodeTile ports opj_tcd_init_decode_tile: allocate the geometry (bands,
// precincts, code-blocks, tag trees, stepsizes) for decoding the given tile.
func (t *TCD) InitDecodeTile(tileNo uint32, mgr *event.Manager) error {
	return t.initTile(tileNo, false, mgr)
}

// InitEncodeTile ports opj_tcd_init_encode_tile. Encode is out of W7 scope.
func (t *TCD) InitEncodeTile(tileNo uint32, mgr *event.Manager) error {
	return t.initTile(tileNo, true, mgr)
}
