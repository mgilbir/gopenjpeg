package mqc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// --- vector file schema ------------------------------------------------

type encCase struct {
	Name     string   `json:"name"`
	Term     string   `json:"term"`
	Ctxs     []uint32 `json:"ctxs"`
	Bits     []uint32 `json:"bits"`
	Out      string   `json:"out"`
	NumBytes uint32   `json:"numbytes"`
}

type decCase struct {
	Name  string   `json:"name"`
	In    string   `json:"in"`
	Len   int      `json:"len"`
	Ctxs  []uint32 `json:"ctxs"`
	Bits  []uint32 `json:"bits"`
	Eobsc uint32   `json:"eobsc"`
}

type bypassCase struct {
	Name     string   `json:"name"`
	Ctxs     []uint32 `json:"ctxs"`
	Bits     []uint32 `json:"bits"`
	BpBits   []uint32 `json:"bpbits"`
	Erterm   int      `json:"erterm"`
	Out      string   `json:"out"`
	NumBytes uint32   `json:"numbytes"`
}

type rawCase struct {
	Name  string   `json:"name"`
	In    string   `json:"in"`
	Len   int      `json:"len"`
	Count int      `json:"count"`
	Bits  []uint32 `json:"bits"`
}

type vectors struct {
	Enc    []encCase    `json:"enc"`
	Dec    []decCase    `json:"dec"`
	Bypass []bypassCase `json:"bypass"`
	Raw    []rawCase    `json:"raw"`
}

func loadVectors(t testing.TB) *vectors {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "vectors", "mqc", "mqc_vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return &v
}

// resetStd applies the standard T1 initial context states used by both the
// encoder and decoder (opj_mqc_resetstates + the three setstates).
func resetStd(m *MQC) { m.ResetEnc() }

// --- encoder vectors ---------------------------------------------------

func TestEncodeVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Enc {
		t.Run(c.Name, func(t *testing.T) {
			var m MQC
			m.InitEnc()
			resetStd(&m)
			for i := range c.Bits {
				m.SetCurCtx(c.Ctxs[i])
				m.Encode(c.Bits[i])
			}
			switch c.Term {
			case "flush":
				m.Flush()
			case "erterm":
				m.ErtermEnc()
			case "segmark":
				m.SegmarkEnc()
				m.Flush()
			default:
				t.Fatalf("unknown term %q", c.Term)
			}
			want, _ := hex.DecodeString(c.Out)
			if got := m.Bytes(); !bytes.Equal(got, want) {
				t.Fatalf("bytes mismatch\n got=%x\nwant=%x", got, want)
			}
			if m.NumBytes() != c.NumBytes {
				t.Fatalf("numbytes=%d want %d", m.NumBytes(), c.NumBytes)
			}
		})
	}
}

// --- decoder vectors (round-trip + adversarial) ------------------------

func TestDecodeVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Dec {
		t.Run(c.Name, func(t *testing.T) {
			in, _ := hex.DecodeString(c.In)
			buf := make([]byte, c.Len+cblkDataExtra)
			copy(buf, in)
			var m MQC
			resetStd(&m)
			m.InitDec(buf, c.Len)
			for i := range c.Ctxs {
				m.SetCurCtx(c.Ctxs[i])
				got := m.Decode()
				if got != c.Bits[i] {
					t.Fatalf("bit[%d]=%d want %d", i, got, c.Bits[i])
				}
			}
			if m.EndOfByteStreamCounter() != c.Eobsc {
				t.Fatalf("eobsc=%d want %d", m.EndOfByteStreamCounter(), c.Eobsc)
			}
			m.FinishDec()
			// FinishDec must restore the scratch bytes we overwrote.
			for i := c.Len; i < c.Len+cblkDataExtra; i++ {
				if buf[i] != 0 {
					t.Fatalf("scratch byte %d not restored: %#x", i, buf[i])
				}
			}
		})
	}
}

// --- bypass (RAW encode) vectors ---------------------------------------

func TestBypassVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Bypass {
		t.Run(c.Name, func(t *testing.T) {
			var m MQC
			m.InitEnc()
			resetStd(&m)
			for i := range c.Bits {
				m.SetCurCtx(c.Ctxs[i])
				m.Encode(c.Bits[i])
			}
			m.Flush()
			m.BypassInitEnc()
			for _, b := range c.BpBits {
				m.BypassEnc(b)
			}
			m.BypassFlushEnc(c.Erterm != 0)
			want, _ := hex.DecodeString(c.Out)
			if got := m.Bytes(); !bytes.Equal(got, want) {
				t.Fatalf("bytes mismatch\n got=%x\nwant=%x", got, want)
			}
			if m.NumBytes() != c.NumBytes {
				t.Fatalf("numbytes=%d want %d", m.NumBytes(), c.NumBytes)
			}
		})
	}
}

// --- raw decode vectors ------------------------------------------------

func TestRawDecodeVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Raw {
		t.Run(c.Name, func(t *testing.T) {
			in, _ := hex.DecodeString(c.In)
			buf := make([]byte, c.Len+cblkDataExtra)
			copy(buf, in)
			var m MQC
			m.RawInitDec(buf, c.Len)
			for i := 0; i < c.Count; i++ {
				got := m.RawDecode()
				if got != c.Bits[i] {
					t.Fatalf("bit[%d]=%d want %d", i, got, c.Bits[i])
				}
			}
			m.FinishDec()
		})
	}
}

// --- round-trip property tests -----------------------------------------

func TestRoundTripMQ(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(600)
		ctxs := make([]uint32, n)
		bits := make([]uint32, n)
		for i := 0; i < n; i++ {
			ctxs[i] = uint32(rng.Intn(19))
			bits[i] = uint32(rng.Intn(2))
		}
		var enc MQC
		enc.InitEnc()
		resetStd(&enc)
		for i := 0; i < n; i++ {
			enc.SetCurCtx(ctxs[i])
			enc.Encode(bits[i])
		}
		enc.Flush()
		coded := enc.Bytes()
		nb := int(enc.NumBytes())

		buf := make([]byte, nb+cblkDataExtra)
		copy(buf, coded)
		var dec MQC
		resetStd(&dec)
		dec.InitDec(buf, nb)
		for i := 0; i < n; i++ {
			dec.SetCurCtx(ctxs[i])
			if got := dec.Decode(); got != bits[i] {
				t.Fatalf("iter %d bit %d: got %d want %d", iter, i, got, bits[i])
			}
		}
		dec.FinishDec()
	}
}

func TestRoundTripRaw(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 200; iter++ {
		// Establish a valid buffer prefix with a flush, then bypass-encode.
		var enc MQC
		enc.InitEnc()
		resetStd(&enc)
		// Small MQ prefix so bypass_init preconditions hold.
		for i := 0; i < 4; i++ {
			enc.SetCurCtx(uint32(rng.Intn(19)))
			enc.Encode(uint32(rng.Intn(2)))
		}
		enc.Flush()
		prefix := int(enc.NumBytes())

		n := 1 + rng.Intn(300)
		bits := make([]uint32, n)
		for i := 0; i < n; i++ {
			bits[i] = uint32(rng.Intn(2))
		}
		enc.BypassInitEnc()
		for _, b := range bits {
			enc.BypassEnc(b)
		}
		enc.BypassFlushEnc(false)
		coded := append([]byte(nil), enc.Bytes()...)

		// Raw-decode the bypass portion; it must reproduce the bits.
		rawPart := coded[prefix:]
		buf := make([]byte, len(rawPart)+cblkDataExtra)
		copy(buf, rawPart)
		var dec MQC
		dec.RawInitDec(buf, len(rawPart))
		for i := 0; i < n; i++ {
			if got := dec.RawDecode(); got != bits[i] {
				t.Fatalf("iter %d bit %d: got %d want %d", iter, i, got, bits[i])
			}
		}
		dec.FinishDec()
	}
}

// --- fuzz: decoder must never panic or read out of bounds --------------

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0xff, 0xff, 0x00}, uint16(0x1234), 32)
	f.Add([]byte{}, uint16(0), 16)
	f.Add([]byte{0xff, 0x90, 0x00, 0x2a}, uint16(0xabcd), 64)
	f.Fuzz(func(t *testing.T, data []byte, ctxSeed uint16, nsym int) {
		if nsym < 0 {
			nsym = -nsym
		}
		nsym %= 4096
		buf := make([]byte, len(data)+cblkDataExtra)
		copy(buf, data)

		var m MQC
		resetStd(&m)
		m.InitDec(buf, len(data))
		ctx := uint32(ctxSeed)
		for i := 0; i < nsym; i++ {
			m.SetCurCtx(ctx % numCtxs)
			ctx = ctx*1103515245 + 12345
			_ = m.Decode()
		}
		m.FinishDec()

		// RAW path over the same bytes.
		copy(buf, data)
		var r MQC
		r.RawInitDec(buf, len(data))
		for i := 0; i < nsym; i++ {
			_ = r.RawDecode()
		}
		r.FinishDec()
	})
}

// --- benchmarks --------------------------------------------------------

func benchData(tb testing.TB) (coded []byte, ctxs []uint32, n int) {
	rng := rand.New(rand.NewSource(42))
	n = 20000
	ctxs = make([]uint32, n)
	bits := make([]uint32, n)
	for i := 0; i < n; i++ {
		ctxs[i] = uint32(rng.Intn(19))
		bits[i] = uint32(rng.Intn(2))
	}
	var enc MQC
	enc.InitEnc()
	resetStd(&enc)
	for i := 0; i < n; i++ {
		enc.SetCurCtx(ctxs[i])
		enc.Encode(bits[i])
	}
	enc.Flush()
	coded = append([]byte(nil), enc.Bytes()...)
	return coded, ctxs, n
}

func BenchmarkDecode(b *testing.B) {
	coded, ctxs, n := benchData(b)
	buf := make([]byte, len(coded)+cblkDataExtra)
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(buf, coded)
		var m MQC
		resetStd(&m)
		m.InitDec(buf, len(coded))
		for j := 0; j < n; j++ {
			m.SetCurCtx(ctxs[j])
			_ = m.Decode()
		}
		m.FinishDec()
	}
}

func BenchmarkEncode(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	n := 20000
	ctxs := make([]uint32, n)
	bits := make([]uint32, n)
	for i := 0; i < n; i++ {
		ctxs[i] = uint32(rng.Intn(19))
		bits[i] = uint32(rng.Intn(2))
	}
	var m MQC
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.InitEnc()
		resetStd(&m)
		for j := 0; j < n; j++ {
			m.SetCurCtx(ctxs[j])
			m.Encode(bits[j])
		}
		m.Flush()
	}
}
