package gopenjpeg

import (
	"fmt"
	"io"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
	"github.com/mgilbir/gopenjpeg/internal/jp2"
)

// ProgressionOrder identifies a packet progression order for encoding.
type ProgressionOrder int32

// Progression orders, mirroring OPJ_PROG_ORDER.
const (
	ProgLRCP ProgressionOrder = ProgressionOrder(cparams.LRCP)
	ProgRLCP ProgressionOrder = ProgressionOrder(cparams.RLCP)
	ProgRPCL ProgressionOrder = ProgressionOrder(cparams.RPCL)
	ProgPCRL ProgressionOrder = ProgressionOrder(cparams.PCRL)
	ProgCPRL ProgressionOrder = ProgressionOrder(cparams.CPRL)
)

// mctSentinel marks "MCT mode not set by the caller" (opj_compress uses 255).
const mctSentinel = 255

// encodeOptions holds the assembled compression configuration.
type encodeOptions struct {
	params    j2k.CParameters
	format    Format
	plt       bool
	tlm       bool
	guardBits int
	jpipOn    bool
	onWarn    func(string)
	onError   func(string)
	onInfo    func(string)
}

func defaultEncodeOptions() encodeOptions {
	var o encodeOptions
	o.format = FormatJ2K
	o.guardBits = -1
	p := &o.params
	p.Rsiz = cparams.ProfileNone
	p.NumResolution = 6
	p.CblockWInit = 64
	p.CblockHInit = 64
	p.ProgOrder = cparams.LRCP
	p.RoiCompno = -1
	p.SubsamplingDx = 1
	p.SubsamplingDy = 1
	p.TcpMct = mctSentinel
	return o
}

// EncodeOption configures an Encode call. The options mirror the codestream
// capabilities of opj_compress.
type EncodeOption func(*encodeOptions)

// WithEncodeFormat selects the output container: FormatJ2K for a raw codestream
// (.j2k/.j2c) or FormatJP2 for a JP2 box-structured file. The default is
// FormatJ2K.
func WithEncodeFormat(f Format) EncodeOption {
	return func(o *encodeOptions) { o.format = f }
}

// WithLossless selects reversible (5/3) coding. This is the default.
func WithLossless() EncodeOption {
	return func(o *encodeOptions) { o.params.Irreversible = 0 }
}

// WithIrreversible selects irreversible (9/7) coding (the -I flag).
func WithIrreversible() EncodeOption {
	return func(o *encodeOptions) { o.params.Irreversible = 1 }
}

// WithRates sets the per-layer compression ratios (the -r flag): rates[i] is the
// target ratio for layer i; a rate of 1 (or <= 1) forces lossless for that
// layer. This selects rate/distortion allocation.
func WithRates(rates ...float32) EncodeOption {
	return func(o *encodeOptions) {
		o.params.CpDistoAlloc = 1
		o.params.TcpNumlayers = int32(len(rates))
		for i, r := range rates {
			if i < len(o.params.TcpRates) {
				o.params.TcpRates[i] = r
			}
		}
	}
}

// WithQualityLayers sets the per-layer target PSNR values in dB (the -q flag),
// selecting fixed-quality allocation.
func WithQualityLayers(psnr ...float32) EncodeOption {
	return func(o *encodeOptions) {
		o.params.CpFixedQuality = 1
		o.params.TcpNumlayers = int32(len(psnr))
		for i, q := range psnr {
			if i < len(o.params.TcpDistoratio) {
				o.params.TcpDistoratio[i] = q
			}
		}
	}
}

// WithResolutions sets the number of resolution levels (decompositions+1), the
// -n flag.
func WithResolutions(n int) EncodeOption {
	return func(o *encodeOptions) { o.params.NumResolution = int32(n) }
}

// WithCodeBlockSize sets the code-block width and height (the -b flag). Both
// must be powers of two in [4,1024] with area <= 4096.
func WithCodeBlockSize(w, h int) EncodeOption {
	return func(o *encodeOptions) { o.params.CblockWInit = int32(w); o.params.CblockHInit = int32(h) }
}

// WithTileSize sets an explicit tile width and height (the -t flag).
func WithTileSize(w, h int) EncodeOption {
	return func(o *encodeOptions) {
		o.params.TileSizeOn = true
		o.params.CpTdx = int32(w)
		o.params.CpTdy = int32(h)
	}
}

// WithTileOrigin sets the tiling origin (the -T flag).
func WithTileOrigin(x, y int) EncodeOption {
	return func(o *encodeOptions) { o.params.CpTx0 = int32(x); o.params.CpTy0 = int32(y) }
}

// WithProgressionOrder sets the packet progression order (the -p flag).
func WithProgressionOrder(order ProgressionOrder) EncodeOption {
	return func(o *encodeOptions) { o.params.ProgOrder = cparams.ProgOrder(order) }
}

// WithPrecincts sets the precinct sizes per resolution (the -c flag). Each pair
// is (width, height); the last is repeated for finer resolutions. This enables
// custom precinct partitioning.
func WithPrecincts(sizes ...[2]int) EncodeOption {
	return func(o *encodeOptions) {
		o.params.Csty |= 0x01
		o.params.ResSpec = int32(len(sizes))
		for i, s := range sizes {
			if i < len(o.params.PrcwInit) {
				o.params.PrcwInit[i] = int32(s[0])
				o.params.PrchInit[i] = int32(s[1])
			}
		}
	}
}

// WithSubsampling sets the component sub-sampling factors (the -s flag).
func WithSubsampling(dx, dy int) EncodeOption {
	return func(o *encodeOptions) { o.params.SubsamplingDx = int32(dx); o.params.SubsamplingDy = int32(dy) }
}

// WithSOP enables SOP (start-of-packet) markers (the -SOP flag).
func WithSOP() EncodeOption { return func(o *encodeOptions) { o.params.Csty |= 0x02 } }

// WithEPH enables EPH (end-of-packet-header) markers (the -EPH flag).
func WithEPH() EncodeOption { return func(o *encodeOptions) { o.params.Csty |= 0x04 } }

// WithModeSwitches sets the code-block style bitmask (the -M flag): the OR of
// LAZY(1), RESET(2), TERMALL(4), VSC(8), PTERM(16), SEGSYM(32).
func WithModeSwitches(mode int) EncodeOption {
	return func(o *encodeOptions) { o.params.Mode = int32(mode) }
}

// WithROI sets a region-of-interest up-shift on a component (the -ROI flag).
func WithROI(compno, shift int) EncodeOption {
	return func(o *encodeOptions) { o.params.RoiCompno = int32(compno); o.params.RoiShift = int32(shift) }
}

// WithMCT sets the multi-component transform mode: 0 none, 1 RCT/ICT, 2 custom
// (requires WithCustomMCT). Mirrors the -mct flag.
func WithMCT(mode int) EncodeOption {
	return func(o *encodeOptions) { o.params.TcpMct = int32(mode) }
}

// WithCustomMCT sets a Part-2 array-based MCT: matrix is a numcomps*numcomps
// row-major coding matrix and dcShift holds the numcomps DC offsets (the -m
// flag). It forces irreversible coding and the Part-2/MCT profile, mirroring
// opj_set_MCT.
func WithCustomMCT(matrix []float32, dcShift []int32) EncodeOption {
	return func(o *encodeOptions) {
		if cparams.IsPart2(o.params.Rsiz) {
			o.params.Rsiz |= cparams.ExtensionMCT
		} else {
			o.params.Rsiz = uint16(cparams.ProfilePart2 | cparams.ExtensionMCT)
		}
		o.params.Irreversible = 1
		o.params.TcpMct = 2
		o.params.MctData = append([]float32(nil), matrix...)
		o.params.MctDcShift = append([]int32(nil), dcShift...)
	}
}

// WithTileParts enables tile-part generation divided on the given flag: 'R'
// (resolution), 'L' (layer), 'C' (component) — the -TP flag.
func WithTileParts(flag byte) EncodeOption {
	return func(o *encodeOptions) { o.params.TpOn = 1; o.params.TpFlag = flag }
}

// WithPOC appends a progression-order change (the -POC flag).
func WithPOC(pocs ...POCChange) EncodeOption {
	return func(o *encodeOptions) {
		o.params.Numpocs = uint32(len(pocs))
		for i, pc := range pocs {
			if i >= len(o.params.POC) {
				break
			}
			o.params.POC[i] = j2k.POCParam{
				Tile:    pc.Tile,
				Resno0:  pc.ResStart,
				Compno0: pc.CompStart,
				Layno1:  pc.LayEnd,
				Resno1:  pc.ResEnd,
				Compno1: pc.CompEnd,
				Prg1:    cparams.ProgOrder(pc.Order),
			}
		}
	}
}

// POCChange is one progression-order-change record for WithPOC.
type POCChange struct {
	Tile      uint32
	ResStart  uint32
	CompStart uint32
	LayEnd    uint32
	ResEnd    uint32
	CompEnd   uint32
	Order     ProgressionOrder
}

// WithComment sets the COM marker payload (the -C flag). An empty string
// suppresses the default "Created by OpenJPEG" comment.
func WithComment(s string) EncodeOption {
	return func(o *encodeOptions) { o.params.CpComment = &s }
}

// WithCinema2K selects the 2K Digital Cinema profile at the given frame rate
// (24 or 48), the -cinema2K flag.
func WithCinema2K(fps int) EncodeOption {
	return func(o *encodeOptions) {
		o.params.Rsiz = cparams.ProfileCinema2K
		if fps == 48 {
			o.params.MaxCompSize = 520833 // OPJ_CINEMA_48_COMP
			o.params.MaxCsSize = 651041   // OPJ_CINEMA_48_CS
		} else {
			o.params.MaxCompSize = 1041666 // OPJ_CINEMA_24_COMP
			o.params.MaxCsSize = 1302083   // OPJ_CINEMA_24_CS
		}
	}
}

// WithCinema4K selects the 4K Digital Cinema profile (the -cinema4K flag).
func WithCinema4K() EncodeOption {
	return func(o *encodeOptions) { o.params.Rsiz = cparams.ProfileCinema4K }
}

// WithProfile sets the raw Rsiz profile value (e.g. an IMF profile with
// main/sub level bits), the -IMF flag maps here.
func WithProfile(rsiz uint16) EncodeOption {
	return func(o *encodeOptions) { o.params.Rsiz = rsiz }
}

// WithMaxCodestreamSize caps the total codestream size in bytes (the -CS flag).
func WithMaxCodestreamSize(n int) EncodeOption {
	return func(o *encodeOptions) { o.params.MaxCsSize = int32(n) }
}

// WithMaxComponentSize caps the per-component size in bytes.
func WithMaxComponentSize(n int) EncodeOption {
	return func(o *encodeOptions) { o.params.MaxCompSize = int32(n) }
}

// WithPLT enables PLT (packet length, tile-part header) marker emission (the
// -PLT flag).
func WithPLT() EncodeOption { return func(o *encodeOptions) { o.plt = true } }

// WithTLM enables TLM (tile-part length, main header) marker emission (the -TLM
// flag).
func WithTLM() EncodeOption { return func(o *encodeOptions) { o.tlm = true } }

// WithGuardBits sets the number of quantisation guard bits in [0,7] (the -G
// flag).
func WithGuardBits(n int) EncodeOption { return func(o *encodeOptions) { o.guardBits = n } }

// WithEncodeWarningHandler installs a callback for encoder warnings.
func WithEncodeWarningHandler(fn func(string)) EncodeOption {
	return func(o *encodeOptions) { o.onWarn = fn }
}

// WithEncodeErrorHandler installs a callback for encoder error diagnostics.
func WithEncodeErrorHandler(fn func(string)) EncodeOption {
	return func(o *encodeOptions) { o.onError = fn }
}

// WithEncodeInfoHandler installs a callback for informational messages.
func WithEncodeInfoHandler(fn func(string)) EncodeOption {
	return func(o *encodeOptions) { o.onInfo = fn }
}

func (o *encodeOptions) manager() *event.Manager {
	if o.onWarn == nil && o.onError == nil && o.onInfo == nil {
		return nil
	}
	return &event.Manager{
		ErrorHandler:   o.onError,
		WarningHandler: o.onWarn,
		InfoHandler:    o.onInfo,
	}
}

// extraOptions builds the PLT/TLM/GUARD_BITS extra-option strings the CLI passes
// to opj_encoder_set_extra_options.
func (o *encodeOptions) extraOptions() []string {
	var opts []string
	if o.plt {
		opts = append(opts, "PLT=YES")
	}
	if o.tlm {
		opts = append(opts, "TLM=YES")
	}
	if o.guardBits >= 0 {
		opts = append(opts, fmt.Sprintf("GUARD_BITS=%d", o.guardBits))
	}
	return opts
}

// Encode compresses img to w as a JPEG 2000 codestream (FormatJ2K, the default)
// or JP2 container (FormatJP2, via WithEncodeFormat). The options mirror the
// codestream capabilities of opj_compress. The whole output is assembled in
// memory before being written to w (the JP2 jp2c length back-patch needs a
// seekable stream).
func Encode(img *Image, w io.Writer, opts ...EncodeOption) error {
	if img == nil {
		return fmt.Errorf("gopenjpeg: encode: nil image")
	}
	o := defaultEncodeOptions()
	for _, fn := range opts {
		fn(&o)
	}

	internalImg := img.internal()

	// Resolve the MCT default the way opj_compress does.
	if o.params.TcpMct == mctSentinel {
		if internalImg.Numcomps >= 3 {
			o.params.TcpMct = 1
		} else {
			o.params.TcpMct = 0
		}
	}

	mgr := o.manager()
	stream := cio.NewMemoryOutputStream()

	var err error
	switch o.format {
	case FormatJP2:
		err = encodeJP2(stream, internalImg, &o, mgr)
	default:
		err = encodeJ2K(stream, internalImg, &o, mgr)
	}
	if err != nil {
		return err
	}
	if _, werr := w.Write(stream.Bytes()); werr != nil {
		return fmt.Errorf("gopenjpeg: encode: write output: %w", werr)
	}
	return nil
}

func encodeJ2K(stream *cio.Stream, img *image.Image, o *encodeOptions, mgr *event.Manager) error {
	enc := j2k.CreateCompress()
	if err := enc.SetupEncoder(&o.params, img, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: setup encoder: %w", err)
	}
	if extra := o.extraOptions(); len(extra) > 0 {
		if err := enc.EncoderSetExtraOptions(extra, mgr); err != nil {
			return fmt.Errorf("gopenjpeg: encoder options: %w", err)
		}
	}
	if err := enc.StartCompress(stream, img, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: start compress: %w", err)
	}
	if err := enc.Encode(stream, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: encode: %w", err)
	}
	if err := enc.EndCompress(stream, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: end compress: %w", err)
	}
	return nil
}

func encodeJP2(stream *cio.Stream, img *image.Image, o *encodeOptions, mgr *event.Manager) error {
	adapter := newJ2KEncodeAdapter(&o.params, o.extraOptions())
	container := jp2.Create(adapter, false)
	if err := container.SetupEncoder(&jp2.EncoderParams{JpipOn: o.jpipOn}, img, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: setup jp2 encoder: %w", err)
	}
	if err := container.StartCompress(stream, img, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: start compress: %w", err)
	}
	if err := container.Encode(stream, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: encode: %w", err)
	}
	if err := container.EndCompress(stream, mgr); err != nil {
		return fmt.Errorf("gopenjpeg: end compress: %w", err)
	}
	return nil
}
