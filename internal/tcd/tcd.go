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
	"github.com/mgilbir/gopenjpeg/internal/t1"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// Approximate C struct sizes, used only to preserve the overflow guards of
// opj_tcd_init_tile that reject absurd code-block counts before allocation.
const (
	sizeofCblkDec = 88
	sizeofCblkEnc = 88
)

// HTDecodeCblk is the hook for HTJ2K (High Throughput) code-block decoding. The
// internal/ht package is developed separately; the coordinator wires this hook
// once it lands. When nil, tcd returns an error for HT-styled code-blocks.
//
// The signature mirrors what tcd needs, paralleling t1's DecodeCblk: it must
// decode the code-block into t1State (updating its w/h/data) or into
// cblk.DecodedData, exactly as opj_t1_ht_decode_cblk does.
var HTDecodeCblk func(t1State *t1.T1, cblk *t1.CodeBlockDec, orient, roishift, cblksty uint32, checkPterm bool) (bool, error)

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
}

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
