package pi

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// packet is one (compno, resno, precno, layno) tuple produced by the iterator.
type packet struct {
	c, r, p, l uint32
}

type pocRec struct {
	prg                                      cparams.ProgOrder
	resno0, resno1, compno0, compno1, layno1 uint32
}

type piConfig struct {
	name                               string
	imgX0, imgY0, imgX1, imgY1         uint32
	numcomps                           uint32
	dx, dy                             []uint32
	numres                             []uint32
	prcw, prch                         [][]uint32
	tw, th, tdx, tdy, tx0, ty0, tileno uint32
	prg                                cparams.ProgOrder
	numlayers                          uint32
	usePoc                             bool
	numpocs                            uint32
	pocs                               []pocRec
	decode, encode                     []packet
}

func (cfg *piConfig) buildImage() *image.Image {
	img := &image.Image{
		X0:       cfg.imgX0,
		Y0:       cfg.imgY0,
		X1:       cfg.imgX1,
		Y1:       cfg.imgY1,
		Numcomps: cfg.numcomps,
		Comps:    make([]image.Comp, cfg.numcomps),
	}
	for i := uint32(0); i < cfg.numcomps; i++ {
		img.Comps[i].Dx = cfg.dx[i]
		img.Comps[i].Dy = cfg.dy[i]
	}
	return img
}

func (cfg *piConfig) buildCP() *cparams.CP {
	cp := &cparams.CP{
		Rsiz: cparams.ProfileNone,
		Tx0:  cfg.tx0,
		Ty0:  cfg.ty0,
		Tdx:  cfg.tdx,
		Tdy:  cfg.tdy,
		Tw:   cfg.tw,
		Th:   cfg.th,
		Tcps: make([]cparams.TCP, cfg.tw*cfg.th),
	}
	tcp := &cp.Tcps[cfg.tileno]
	tcp.Prg = cfg.prg
	tcp.Numlayers = cfg.numlayers
	tcp.NumLayersToDecode = cfg.numlayers
	tcp.TCCPs = make([]cparams.TCCP, cfg.numcomps)
	for c := uint32(0); c < cfg.numcomps; c++ {
		tccp := &tcp.TCCPs[c]
		tccp.Numresolutions = cfg.numres[c]
		for r := uint32(0); r < cparams.MaxRLvls; r++ {
			tccp.Prcw[r] = 15
			tccp.Prch[r] = 15
		}
		for r := uint32(0); r < cfg.numres[c]; r++ {
			tccp.Prcw[r] = cfg.prcw[c][r]
			tccp.Prch[r] = cfg.prch[c][r]
		}
	}
	if cfg.usePoc {
		tcp.POC = 1
		tcp.Numpocs = cfg.numpocs
		for i := uint32(0); i <= cfg.numpocs; i++ {
			p := &tcp.Pocs[i]
			p.Prg = cfg.pocs[i].prg
			p.Prg1 = cfg.pocs[i].prg
			p.Resno0 = cfg.pocs[i].resno0
			p.Resno1 = cfg.pocs[i].resno1
			p.Compno0 = cfg.pocs[i].compno0
			p.Compno1 = cfg.pocs[i].compno1
			p.Layno1 = cfg.pocs[i].layno1
			p.Precno0 = 0
			p.Precno1 = 1
		}
	}
	return cp
}

func parsePiVectors(t *testing.T, path string) []piConfig {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var configs []piConfig

	fields := func(line string) []string { return strings.Fields(line) }
	mustU32 := func(s string) uint32 {
		var v uint32
		fmt.Sscanf(s, "%d", &v)
		return v
	}

	if !sc.Scan() {
		t.Fatal("empty vector file")
	}
	// header: PIVEC 1 N
	var cur *piConfig
	readSeq := func(n int) []packet {
		seq := make([]packet, 0, n)
		for i := 0; i < n; i++ {
			if !sc.Scan() {
				t.Fatal("unexpected EOF reading sequence")
			}
			fl := fields(sc.Text())
			seq = append(seq, packet{mustU32(fl[0]), mustU32(fl[1]), mustU32(fl[2]), mustU32(fl[3])})
		}
		return seq
	}

	for sc.Scan() {
		fl := fields(sc.Text())
		if len(fl) == 0 {
			continue
		}
		switch fl[0] {
		case "CONFIG":
			configs = append(configs, piConfig{name: fl[1]})
			cur = &configs[len(configs)-1]
		case "IMG":
			cur.imgX0, cur.imgY0, cur.imgX1, cur.imgY1 = mustU32(fl[1]), mustU32(fl[2]), mustU32(fl[3]), mustU32(fl[4])
		case "COMPS":
			cur.numcomps = mustU32(fl[1])
			cur.dx = make([]uint32, cur.numcomps)
			cur.dy = make([]uint32, cur.numcomps)
			cur.numres = make([]uint32, cur.numcomps)
			cur.prcw = make([][]uint32, cur.numcomps)
			cur.prch = make([][]uint32, cur.numcomps)
		case "COMP":
			c := mustU32(fl[1])
			cur.dx[c] = mustU32(fl[2])
			cur.dy[c] = mustU32(fl[3])
			cur.numres[c] = mustU32(fl[4])
			cur.prcw[c] = make([]uint32, cur.numres[c])
			cur.prch[c] = make([]uint32, cur.numres[c])
		case "RES":
			c, r := mustU32(fl[1]), mustU32(fl[2])
			cur.prcw[c][r] = mustU32(fl[3])
			cur.prch[c][r] = mustU32(fl[4])
		case "TILE":
			cur.tw, cur.th, cur.tdx, cur.tdy = mustU32(fl[1]), mustU32(fl[2]), mustU32(fl[3]), mustU32(fl[4])
			cur.tx0, cur.ty0, cur.tileno = mustU32(fl[5]), mustU32(fl[6]), mustU32(fl[7])
		case "PRG":
			var prg int32
			fmt.Sscanf(fl[1], "%d", &prg)
			cur.prg = cparams.ProgOrder(prg)
			cur.numlayers = mustU32(fl[2])
		case "POC":
			cur.usePoc = mustU32(fl[1]) != 0
			cur.numpocs = mustU32(fl[2])
			if cur.usePoc {
				cur.pocs = make([]pocRec, cur.numpocs+1)
			}
		case "POCLINE":
			i := mustU32(fl[1])
			var prg int32
			fmt.Sscanf(fl[2], "%d", &prg)
			cur.pocs[i] = pocRec{
				prg:     cparams.ProgOrder(prg),
				resno0:  mustU32(fl[3]),
				resno1:  mustU32(fl[4]),
				compno0: mustU32(fl[5]),
				compno1: mustU32(fl[6]),
				layno1:  mustU32(fl[7]),
			}
		case "DECODE":
			cur.decode = readSeq(int(mustU32(fl[1])))
		case "ENCODE":
			cur.encode = readSeq(int(mustU32(fl[1])))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return configs
}

func goDecodeSeq(cfg *piConfig) []packet {
	img := cfg.buildImage()
	cp := cfg.buildCP()
	pis := CreateDecode(img, cp, cfg.tileno, nil)
	if pis == nil {
		return nil
	}
	var seq []packet
	for pino := uint32(0); pino <= cp.Tcps[cfg.tileno].Numpocs; pino++ {
		cur := &pis[pino]
		for cur.Next() {
			seq = append(seq, packet{cur.compno, cur.resno, cur.precno, cur.layno})
		}
	}
	return seq
}

func goEncodeSeq(cfg *piConfig) []packet {
	img := cfg.buildImage()
	cp := cfg.buildCP()
	pis := InitialiseEncode(img, cp, cfg.tileno, cparams.FinalPass, nil)
	if pis == nil {
		return nil
	}
	tppos := cp.MEnc.MTpPos
	var seq []packet
	for pino := uint32(0); pino <= cp.Tcps[cfg.tileno].Numpocs; pino++ {
		CreateEncode(pis, cp, cfg.tileno, pino, 0, tppos, cparams.FinalPass)
		cur := &pis[pino]
		for cur.Next() {
			seq = append(seq, packet{cur.compno, cur.resno, cur.precno, cur.layno})
		}
	}
	return seq
}

func seqEqual(a, b []packet) (int, bool) {
	if len(a) != len(b) {
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return i, false
			}
		}
		return n, false
	}
	for i := range a {
		if a[i] != b[i] {
			return i, false
		}
	}
	return 0, true
}

func TestPiVectors(t *testing.T) {
	configs := parsePiVectors(t, "../../testdata/vectors/pi/pi_vectors.txt")
	if len(configs) == 0 {
		t.Fatal("no configs parsed")
	}
	for i := range configs {
		cfg := &configs[i]
		t.Run(cfg.name, func(t *testing.T) {
			gotDec := goDecodeSeq(cfg)
			if idx, ok := seqEqual(gotDec, cfg.decode); !ok {
				t.Fatalf("decode mismatch at %d: got len=%d want len=%d\n got=%v\nwant=%v",
					idx, len(gotDec), len(cfg.decode), sample(gotDec, idx), sample(cfg.decode, idx))
			}
			gotEnc := goEncodeSeq(cfg)
			if idx, ok := seqEqual(gotEnc, cfg.encode); !ok {
				t.Fatalf("encode mismatch at %d: got len=%d want len=%d\n got=%v\nwant=%v",
					idx, len(gotEnc), len(cfg.encode), sample(gotEnc, idx), sample(cfg.encode, idx))
			}
		})
	}
}

// sample returns a few packets around index idx for error reporting.
func sample(s []packet, idx int) []packet {
	lo := idx - 2
	if lo < 0 {
		lo = 0
	}
	hi := idx + 3
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}
