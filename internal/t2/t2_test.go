package t2

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/tgt"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

const encBufSize = 1 << 18

type passDesc struct {
	length uint32
	term   bool
}

type layerDesc struct {
	numpasses uint32
	length    uint32
	data      []byte
}

type cblkDesc struct {
	numbps      uint32
	numlayers   uint32
	totalpasses uint32
	layers      []layerDesc
	passes      []passDesc
}

type bandDesc struct {
	numbps int32
	cw, ch uint32
}

type decSeg struct {
	length, numpasses, realnumpasses uint32
}

type decCblk struct {
	numsegs, realnumsegs uint32
	corrupted            bool
	numchunks            uint32
	segs                 []decSeg
	ddata                []byte
}

type t2Case struct {
	name                                           string
	csty, prg, maxlayers, numcomps, numres, cw, ch uint32
	numlayers, ppl, cblksty, termEach, imgw, imgh  uint32
	ltd, trunc                                     uint32
	strict                                         bool
	bands                                          map[string]bandDesc
	cblks                                          map[string]*cblkDesc
	encOK                                          bool
	encBytes                                       []byte
	decOK                                          bool
	decRead, declen                                uint32
	decCblks                                       map[string]*decCblk
}

func numbandsOf(r uint32) uint32 {
	if r == 0 {
		return 1
	}
	return 3
}

func bandnoOf(r, b uint32) uint32 {
	if r == 0 {
		return 0
	}
	return b + 1
}

func key(parts ...uint32) string {
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", p)
	}
	return sb.String()
}

func hexOrEmpty(s string) []byte {
	if s == "-" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("bad hex: " + s)
	}
	return b
}

func parseT2Vectors(t *testing.T, path string) []*t2Case {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<26), 1<<26)

	u := func(s string) uint32 {
		var v uint32
		fmt.Sscanf(s, "%d", &v)
		return v
	}
	i32 := func(s string) int32 {
		var v int32
		fmt.Sscanf(s, "%d", &v)
		return v
	}

	var cases []*t2Case
	var cur *t2Case

	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) == 0 {
			continue
		}
		switch fl[0] {
		case "T2VEC":
			// header
		case "CASE":
			// CASE name csty X prg X maxlayers X numcomps X numres X cw X ch X
			// numlayers X ppl X cblksty X term_each X imgw X imgh X ltd X trunc X strict X
			c := &t2Case{
				name:     fl[1],
				bands:    map[string]bandDesc{},
				cblks:    map[string]*cblkDesc{},
				decCblks: map[string]*decCblk{},
			}
			m := map[string]string{}
			for i := 2; i+1 < len(fl); i += 2 {
				m[fl[i]] = fl[i+1]
			}
			c.csty = u(m["csty"])
			c.prg = u(m["prg"])
			c.maxlayers = u(m["maxlayers"])
			c.numcomps = u(m["numcomps"])
			c.numres = u(m["numres"])
			c.cw = u(m["cw"])
			c.ch = u(m["ch"])
			c.numlayers = u(m["numlayers"])
			c.ppl = u(m["ppl"])
			c.cblksty = u(m["cblksty"])
			c.termEach = u(m["term_each"])
			c.imgw = u(m["imgw"])
			c.imgh = u(m["imgh"])
			c.ltd = u(m["ltd"])
			c.trunc = u(m["trunc"])
			c.strict = m["strict"] == "1"
			cases = append(cases, c)
			cur = c
		case "BAND":
			// BAND comp res bandno numbps N cw N ch N
			comp, res, bandno := u(fl[1]), u(fl[2]), u(fl[3])
			cur.bands[key(comp, res, bandno)] = bandDesc{
				numbps: i32(fl[5]), cw: u(fl[7]), ch: u(fl[9]),
			}
		case "CBLK":
			// CBLK comp res bandno cblkno numbps N numlayers N totalpasses N
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			cur.cblks[key(comp, res, bandno, cblkno)] = &cblkDesc{
				numbps: u(fl[6]), numlayers: u(fl[8]), totalpasses: u(fl[10]),
			}
		case "LAYER":
			// LAYER comp res bandno cblkno layno np N len N data HEX
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			cb := cur.cblks[key(comp, res, bandno, cblkno)]
			cb.layers = append(cb.layers, layerDesc{
				numpasses: u(fl[7]), length: u(fl[9]), data: hexOrEmpty(fl[11]),
			})
		case "PASS":
			// PASS comp res bandno cblkno passno len N term N
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			cb := cur.cblks[key(comp, res, bandno, cblkno)]
			cb.passes = append(cb.passes, passDesc{length: u(fl[7]), term: fl[9] == "1"})
		case "ENC":
			// ENC ok written HEX
			cur.encOK = fl[1] == "1"
			cur.encBytes = hexOrEmpty(fl[3])
		case "DEC":
			// DEC ok read declen N
			cur.decOK = fl[1] == "1"
			cur.decRead = u(fl[2])
			cur.declen = u(fl[4])
		case "DCBLK":
			// DCBLK comp res bandno cblkno numsegs N realnumsegs N corrupted N numchunks N
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			cur.decCblks[key(comp, res, bandno, cblkno)] = &decCblk{
				numsegs: u(fl[6]), realnumsegs: u(fl[8]),
				corrupted: fl[10] == "1", numchunks: u(fl[12]),
			}
		case "DSEG":
			// DSEG comp res bandno cblkno segno len N numpasses N realnumpasses N
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			dc := cur.decCblks[key(comp, res, bandno, cblkno)]
			dc.segs = append(dc.segs, decSeg{
				length: u(fl[7]), numpasses: u(fl[9]), realnumpasses: u(fl[11]),
			})
		case "DDATA":
			// DDATA comp res bandno cblkno total HEX
			comp, res, bandno, cblkno := u(fl[1]), u(fl[2]), u(fl[3]), u(fl[4])
			dc := cur.decCblks[key(comp, res, bandno, cblkno)]
			dc.ddata = hexOrEmpty(fl[6])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return cases
}

func (c *t2Case) buildImage() *image.Image {
	img := &image.Image{
		X0: 0, Y0: 0, X1: c.imgw, Y1: c.imgh,
		Numcomps: c.numcomps,
		Comps:    make([]image.Comp, c.numcomps),
	}
	for i := uint32(0); i < c.numcomps; i++ {
		img.Comps[i].Dx = 1
		img.Comps[i].Dy = 1
	}
	return img
}

func (c *t2Case) buildCP() *cparams.CP {
	cp := &cparams.CP{
		Rsiz: cparams.ProfileNone,
		Tx0:  0, Ty0: 0, Tdx: c.imgw, Tdy: c.imgh,
		Tw: 1, Th: 1,
		Strict: c.strict,
		Tcps:   make([]cparams.TCP, 1),
	}
	tcp := &cp.Tcps[0]
	tcp.Csty = c.csty
	tcp.Prg = cparams.ProgOrder(c.prg)
	tcp.Numlayers = c.numlayers
	tcp.NumLayersToDecode = c.ltd
	tcp.TCCPs = make([]cparams.TCCP, c.numcomps)
	for comp := uint32(0); comp < c.numcomps; comp++ {
		tccp := &tcp.TCCPs[comp]
		tccp.Numresolutions = c.numres
		tccp.Cblksty = c.cblksty
		tccp.Qmfbid = 1
		for r := uint32(0); r < cparams.MaxRLvls; r++ {
			tccp.Prcw[r] = 15
			tccp.Prch[r] = 15
		}
	}
	return cp
}

// buildTile builds either an encode tile (enc=true, cblks filled from the case)
// or an empty decode tile (enc=false).
func (c *t2Case) buildTile(enc bool) *tile.Tile {
	tl := &tile.Tile{
		X0: 0, Y0: 0, X1: int32(c.imgw), Y1: int32(c.imgh),
		Numcomps: c.numcomps,
		Comps:    make([]tile.TileComp, c.numcomps),
	}
	for comp := uint32(0); comp < c.numcomps; comp++ {
		tc := &tl.Comps[comp]
		tc.Compno = comp
		tc.Numresolutions = c.numres
		tc.MinimumNumResolutions = c.numres
		tc.X0, tc.Y0, tc.X1, tc.Y1 = 0, 0, int32(c.imgw), int32(c.imgh)
		tc.Resolutions = make([]tile.Resolution, c.numres)
		for r := uint32(0); r < c.numres; r++ {
			res := &tc.Resolutions[r]
			res.Pw, res.Ph = 1, 1
			res.Numbands = numbandsOf(r)
			res.X0, res.Y0, res.X1, res.Y1 = 0, 0, 8, 8
			for b := uint32(0); b < res.Numbands; b++ {
				bandno := bandnoOf(r, b)
				bd := c.bands[key(comp, r, bandno)]
				band := &res.Bands[b]
				band.Bandno = bandno
				band.X0, band.Y0, band.X1, band.Y1 = 0, 0, 8, 8
				band.Numbps = bd.numbps
				band.Precincts = make([]tile.Precinct, 1)
				prc := &band.Precincts[0]
				prc.X0, prc.Y0, prc.X1, prc.Y1 = 0, 0, 8, 8
				prc.Cw, prc.Ch = c.cw, c.ch
				incl, err := tgt.Create(c.cw, c.ch, nil)
				if err != nil {
					panic(err)
				}
				imsb, err := tgt.Create(c.cw, c.ch, nil)
				if err != nil {
					panic(err)
				}
				prc.Incltree = incl
				prc.Imsbtree = imsb
				nblk := c.cw * c.ch
				if enc {
					prc.CblksEnc = make([]tile.CblkEnc, nblk)
					for k := uint32(0); k < nblk; k++ {
						cd := c.cblks[key(comp, r, bandno, k)]
						cblk := &prc.CblksEnc[k]
						cblk.X0, cblk.Y0, cblk.X1, cblk.Y1 = 0, 0, 8, 8
						cblk.Numbps = cd.numbps
						cblk.Totalpasses = cd.totalpasses
						cblk.Passes = make([]tile.Pass, len(cd.passes))
						for pi, pd := range cd.passes {
							cblk.Passes[pi].Len = pd.length
							cblk.Passes[pi].Term = pd.term
						}
						cblk.Layers = make([]tile.Layer, len(cd.layers))
						for li, ld := range cd.layers {
							cblk.Layers[li].Numpasses = ld.numpasses
							cblk.Layers[li].Len = ld.length
							cblk.Layers[li].Data = ld.data
						}
					}
				} else {
					prc.CblksDec = make([]tile.CblkDec, nblk)
					for k := uint32(0); k < nblk; k++ {
						cblk := &prc.CblksDec[k]
						cblk.X0, cblk.Y0, cblk.X1, cblk.Y1 = 0, 0, 8, 8
					}
				}
			}
		}
	}
	return tl
}

func TestT2Vectors(t *testing.T) {
	cases := parseT2Vectors(t, "../../testdata/vectors/t2/t2_vectors.txt")
	if len(cases) == 0 {
		t.Fatal("no cases parsed")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// --- encode ---
			img := c.buildImage()
			cp := c.buildCP()
			t2 := Create(img, cp)
			encTile := c.buildTile(true)
			dest := make([]byte, encBufSize)
			written, ok := t2.EncodePackets(0, encTile, c.maxlayers, dest, encBufSize,
				nil, nil, 0, 0, 0, cparams.FinalPass, nil)
			if ok != c.encOK {
				t.Fatalf("encode ok: got %v want %v", ok, c.encOK)
			}
			if ok {
				got := dest[:written]
				if len(got) != len(c.encBytes) {
					t.Fatalf("encode length: got %d want %d\n got=%x\nwant=%x",
						len(got), len(c.encBytes), got, c.encBytes)
				}
				for i := range got {
					if got[i] != c.encBytes[i] {
						t.Fatalf("encode byte %d: got %02x want %02x", i, got[i], c.encBytes[i])
					}
				}
			}

			// --- decode --- (fresh image/cp/t2 mirroring the C harness)
			dimg := c.buildImage()
			dcp := c.buildCP()
			dcp.Strict = c.strict
			d2 := Create(dimg, dcp)
			decTile := c.buildTile(false)
			declen := c.declen
			read, dok := d2.DecodePackets(WholeTileAOI{}, 0, decTile, c.encBytes, declen, nil)
			if dok != c.decOK {
				t.Fatalf("decode ok: got %v want %v", dok, c.decOK)
			}
			if !dok {
				return // hard-error case: nothing further to compare
			}
			if read != c.decRead {
				t.Fatalf("decode read: got %d want %d", read, c.decRead)
			}
			c.compareDecode(t, decTile)
		})
	}
}

func (c *t2Case) compareDecode(t *testing.T, decTile *tile.Tile) {
	for comp := uint32(0); comp < c.numcomps; comp++ {
		tc := &decTile.Comps[comp]
		for r := uint32(0); r < c.numres; r++ {
			res := &tc.Resolutions[r]
			for b := uint32(0); b < res.Numbands; b++ {
				band := &res.Bands[b]
				bandno := band.Bandno
				prc := &band.Precincts[0]
				for k := uint32(0); k < c.cw*c.ch; k++ {
					kk := key(comp, r, bandno, k)
					want := c.decCblks[kk]
					if want == nil {
						t.Fatalf("missing expected decode cblk %s", kk)
					}
					cblk := &prc.CblksDec[k]
					if cblk.Numsegs != want.numsegs || cblk.RealNumSegs != want.realnumsegs ||
						cblk.Corrupted != want.corrupted || cblk.Numchunks != want.numchunks {
						t.Fatalf("cblk %s: got numsegs=%d realsegs=%d corrupted=%v numchunks=%d; want %d %d %v %d",
							kk, cblk.Numsegs, cblk.RealNumSegs, cblk.Corrupted, cblk.Numchunks,
							want.numsegs, want.realnumsegs, want.corrupted, want.numchunks)
					}
					for s := uint32(0); s < cblk.Numsegs; s++ {
						if int(s) >= len(want.segs) {
							t.Fatalf("cblk %s: extra seg %d", kk, s)
						}
						seg := &cblk.Segs[s]
						ws := want.segs[s]
						if seg.Len != ws.length || seg.Numpasses != ws.numpasses || seg.RealNumPasses != ws.realnumpasses {
							t.Fatalf("cblk %s seg %d: got len=%d np=%d rnp=%d; want %d %d %d",
								kk, s, seg.Len, seg.Numpasses, seg.RealNumPasses,
								ws.length, ws.numpasses, ws.realnumpasses)
						}
					}
					// concatenated chunk data
					var got []byte
					for ci := uint32(0); ci < cblk.Numchunks; ci++ {
						got = append(got, cblk.Chunks[ci].Data[:cblk.Chunks[ci].Len]...)
					}
					if len(got) != len(want.ddata) {
						t.Fatalf("cblk %s data len: got %d want %d", kk, len(got), len(want.ddata))
					}
					for i := range got {
						if got[i] != want.ddata[i] {
							t.Fatalf("cblk %s data byte %d: got %02x want %02x", kk, i, got[i], want.ddata[i])
						}
					}
				}
			}
		}
	}
}
