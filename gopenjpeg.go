// Package gopenjpeg is the public API of a pure-Go port of OpenJPEG's decoder.
// It decodes JPEG 2000 images from both raw codestreams (.j2k/.j2c/.jpc, the
// SOC-prefixed format) and JP2 / JPH containers (the box-structured format used
// by .jp2/.jph files), mirroring the capabilities of the reference
// opj_decompress tool: resolution reduction, quality-layer limiting, region
// (decode-area) restriction, component subsetting, single-tile decode, and
// strict/relaxed conformance handling.
//
// # Usage
//
//	img, err := gopenjpeg.Decode(reader,
//	    gopenjpeg.WithReduce(1),
//	    gopenjpeg.WithDecodeArea(0, 0, 256, 256))
//	if err != nil { ... }
//	std, _ := img.ToStandard() // convert to a Go image.Image
//
// The decode is bit-exact with opj_decompress on the supported corpus (see the
// oracletest gate). Colour post-processing that the CLI applies before writing
// (sYCC/CMYK/eYCC to sRGB) is available via Image.ConvertToRGB; the core Decode
// returns the library-level components unchanged.
//
// # Architecture
//
// Decode detects the container format, then drives the internal codestream
// decoder (internal/j2k) directly for raw codestreams, or through the internal
// JP2 container (internal/jp2) for boxed files. The JP2 container talks to the
// codestream decoder through the jp2.CodestreamCodec interface; the adapter
// that satisfies that interface over a *j2k.Decoder lives in this package
// (type j2kAdapter in adapter.go). Only the decode side is implemented; the
// encode methods of the interface return ErrNotImplemented.
package gopenjpeg

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/j2k"
	"github.com/mgilbir/gopenjpeg/internal/jp2"
)

// Format identifies the container/codestream format of the input.
type Format int

// Format values.
const (
	// FormatAuto selects the format from the input's magic bytes: the JP2
	// signature box for boxed files, or the SOC marker (0xFF4F) for raw
	// codestreams.
	FormatAuto Format = iota
	// FormatJ2K is a raw JPEG 2000 codestream (.j2k/.j2c/.jpc), starting with
	// the SOC marker.
	FormatJ2K
	// FormatJP2 is a JP2 / JPH box-structured file (.jp2/.jph), starting with
	// the JPEG 2000 signature box.
	FormatJP2
)

// ErrNotImplemented is returned by the encode side of the internal codec
// adapter, which this decode-only port does not implement.
var ErrNotImplemented = errors.New("gopenjpeg: not implemented (encode path)")

// ErrUnknownFormat is returned when FormatAuto cannot recognise the input.
var ErrUnknownFormat = errors.New("gopenjpeg: unrecognised input format")

// options holds the decode configuration assembled from Option values.
type options struct {
	format  Format
	reduce  uint32
	layers  uint32
	area    *[4]int32
	comps   []uint32
	tile    int64 // -1 == decode whole image
	strict  bool
	threads int
	onWarn  func(string)
	onError func(string)
	onInfo  func(string)
}

func defaultOptions() options {
	return options{format: FormatAuto, tile: -1, threads: 1}
}

// Option configures a decode.
type Option func(*options)

// WithFormat forces the input format instead of auto-detecting it.
func WithFormat(f Format) Option { return func(o *options) { o.format = f } }

// WithReduce discards the reduce highest resolution levels (the -r flag of
// opj_decompress). Each level halves the output dimensions.
func WithReduce(reduce uint32) Option { return func(o *options) { o.reduce = reduce } }

// WithLayers limits decoding to the first layers quality layers (the -l flag).
// Zero (the default) decodes all layers.
func WithLayers(layers uint32) Option { return func(o *options) { o.layers = layers } }

// WithDecodeArea restricts decoding to the reference-grid rectangle
// [x0,y0)-(x1,y1) (the -d flag). Passing all zeros decodes the whole image.
func WithDecodeArea(x0, y0, x1, y1 int32) Option {
	return func(o *options) { o.area = &[4]int32{x0, y0, x1, y1} }
}

// WithComponents decodes only the listed component indices (the -c flag). An
// empty list decodes all components.
func WithComponents(comps ...uint32) Option {
	return func(o *options) { o.comps = append([]uint32(nil), comps...) }
}

// WithTile decodes only the single tile with the given index (the -t flag),
// via the get-decoded-tile path. A negative index (the default) decodes the
// whole image.
func WithTile(index int) Option { return func(o *options) { o.tile = int64(index) } }

// WithStrictMode selects strict conformance: when true, truncated or
// non-compliant codestreams are rejected; when false (the default, matching
// opj_decompress), they are decoded as far as possible.
func WithStrictMode(strict bool) Option { return func(o *options) { o.strict = strict } }

// WithConcurrency sets the number of worker goroutines used for the
// parallelizable decode stages: per-code-block tier-1 decode and the inverse
// DWT row/column passes (mirroring OpenJPEG's opj_thread_pool use in t1.c and
// dwt.c). n<=1 (the default) is fully sequential and bit-for-bit identical to
// C's default single-thread behaviour. n>1 fans work across n goroutines; the
// decoded output is unchanged (workers write to disjoint regions). Pass
// runtime.NumCPU() for the ALL_CPUS equivalent of opj_decompress -threads.
func WithConcurrency(n int) Option {
	return func(o *options) {
		if n < 1 {
			n = 1
		}
		o.threads = n
	}
}

// WithWarningHandler installs a callback for decoder warnings.
func WithWarningHandler(fn func(string)) Option { return func(o *options) { o.onWarn = fn } }

// WithErrorHandler installs a callback for decoder error diagnostics.
func WithErrorHandler(fn func(string)) Option { return func(o *options) { o.onError = fn } }

// WithInfoHandler installs a callback for informational messages.
func WithInfoHandler(fn func(string)) Option { return func(o *options) { o.onInfo = fn } }

func (o *options) manager() *event.Manager {
	if o.onWarn == nil && o.onError == nil && o.onInfo == nil {
		return nil
	}
	return &event.Manager{
		ErrorHandler:   o.onError,
		WarningHandler: o.onWarn,
		InfoHandler:    o.onInfo,
	}
}

// Info carries the header information of a JPEG 2000 image without decoding the
// sample data. It is produced by ReadInfo and used by the gopj-dump command.
type Info struct {
	Format     Format
	X0, Y0     uint32
	X1, Y1     uint32
	TileWidth  uint32
	TileHeight uint32
	TileX0     uint32
	TileY0     uint32
	NumTilesX  uint32
	NumTilesY  uint32
	ColorSpace ColorSpace
	Components []ComponentInfo
	ICCLen     uint32

	// JP2-only box fields (zero for raw codestreams).
	IsJP2        bool
	Brand        uint32
	Meth         uint32
	EnumCS       uint32
	HasPalette   bool
	PaletteChans int
	CdefChannels int
}

// ComponentInfo is the per-component header of an Info.
type ComponentInfo struct {
	Dx, Dy uint32
	Prec   uint32
	Sgnd   bool
}

// openStream builds an input cio.Stream over r. If r is an io.ReadSeeker the
// stream reads directly from it (no full-file copy); otherwise the whole input
// is read into memory. It also returns the leading magic bytes for format
// detection, restoring the read position afterwards.
func openStream(r io.Reader) (s *cio.Stream, magic []byte, cleanup func(), err error) {
	const magicLen = 12
	if rs, ok := r.(io.ReadSeeker); ok {
		hdr := make([]byte, magicLen)
		n, _ := io.ReadFull(rs, hdr)
		if _, serr := rs.Seek(0, io.SeekStart); serr != nil {
			return nil, nil, nil, fmt.Errorf("gopenjpeg: seek input: %w", serr)
		}
		st, serr := cio.NewReadSeekerInputStream(rs)
		if serr != nil {
			return nil, nil, nil, fmt.Errorf("gopenjpeg: open input: %w", serr)
		}
		return st, hdr[:n], func() { st.Destroy() }, nil
	}
	data, rerr := io.ReadAll(r)
	if rerr != nil {
		return nil, nil, nil, fmt.Errorf("gopenjpeg: read input: %w", rerr)
	}
	m := data
	if len(m) > magicLen {
		m = m[:magicLen]
	}
	st := cio.NewMemoryInputStream(data)
	return st, m, func() { st.Destroy() }, nil
}

// detectFormat ports the magic-byte sniffing of opj_decompress/infile_format:
// the 12-byte JP2 signature box, or the SOC marker of a raw codestream.
func detectFormat(magic []byte) Format {
	jp2Sig := []byte{0x00, 0x00, 0x00, 0x0c, 0x6a, 0x50, 0x20, 0x20, 0x0d, 0x0a, 0x87, 0x0a}
	if len(magic) >= 12 && bytes.Equal(magic[:12], jp2Sig) {
		return FormatJP2
	}
	// Some JPH files share the jP.. box; also accept the 8-byte prefix.
	if len(magic) >= 8 && bytes.Equal(magic[4:8], []byte{0x6a, 0x50, 0x20, 0x20}) {
		return FormatJP2
	}
	if len(magic) >= 2 && magic[0] == 0xff && magic[1] == 0x4f {
		return FormatJ2K
	}
	return FormatAuto
}

// Decode reads and decodes a JPEG 2000 image from r. See the Option functions
// for the supported controls. The returned Image holds the library-level
// components (palette expansion, channel mapping and channel definitions are
// already applied for JP2 inputs); call Image.ConvertToRGB to reproduce the
// colour-space conversion opj_decompress performs before writing.
func Decode(r io.Reader, opts ...Option) (*Image, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	stream, magic, cleanup, err := openStream(r)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	format := o.format
	if format == FormatAuto {
		format = detectFormat(magic)
		if format == FormatAuto {
			return nil, ErrUnknownFormat
		}
	}

	mgr := o.manager()

	switch format {
	case FormatJP2:
		return decodeJP2(stream, mgr, &o)
	case FormatJ2K:
		return decodeJ2K(stream, mgr, &o)
	default:
		return nil, ErrUnknownFormat
	}
}

// decodeJ2K drives a raw codestream through internal/j2k directly, mirroring the
// opj_read_header / opj_set_decoded_components / opj_set_decode_area /
// opj_decode (or opj_get_decoded_tile) / opj_end_decompress sequence of
// opj_decompress.
func decodeJ2K(stream *cio.Stream, mgr *event.Manager, o *options) (*Image, error) {
	d := j2k.CreateDecompress()
	d.SetupDecoder(o.reduce, o.layers)
	d.SetStrictMode(o.strict)
	d.SetThreads(o.threads)

	img, err := d.ReadHeader(stream, mgr)
	if err != nil {
		return nil, fmt.Errorf("gopenjpeg: read header: %w", err)
	}
	if len(o.comps) > 0 {
		if err := d.SetDecodedComponents(o.comps); err != nil {
			return nil, fmt.Errorf("gopenjpeg: set components: %w", err)
		}
	}
	if o.tile >= 0 {
		if err := d.GetTile(stream, img, uint32(o.tile), mgr); err != nil {
			return nil, fmt.Errorf("gopenjpeg: decode tile: %w", err)
		}
		return newImage(img), nil
	}
	if err := setDecodeAreaJ2K(d, img, o); err != nil {
		return nil, err
	}
	if err := d.Decode(stream, img, mgr); err != nil {
		return nil, fmt.Errorf("gopenjpeg: decode: %w", err)
	}
	if err := d.EndDecompress(); err != nil {
		return nil, fmt.Errorf("gopenjpeg: end decompress: %w", err)
	}
	return newImage(img), nil
}

func setDecodeAreaJ2K(d *j2k.Decoder, img *image.Image, o *options) error {
	var x0, y0, x1, y1 int32
	if o.area != nil {
		x0, y0, x1, y1 = o.area[0], o.area[1], o.area[2], o.area[3]
	}
	if err := d.SetDecodeArea(img, x0, y0, x1, y1); err != nil {
		return fmt.Errorf("gopenjpeg: set decode area: %w", err)
	}
	return nil
}

// decodeJP2 drives a boxed file through internal/jp2, which in turn calls the
// codestream decoder through the CodestreamCodec adapter.
func decodeJP2(stream *cio.Stream, mgr *event.Manager, o *options) (*Image, error) {
	adapter := newJ2KAdapter(o.reduce, o.layers)
	container := jp2.Create(adapter, true)
	container.SetupDecoder(&jp2.DecoderParams{})
	container.SetDecoderStrictMode(o.strict)
	_ = adapter.SetThreads(uint32(o.threads))

	img, err := container.ReadHeader(stream, mgr)
	if err != nil {
		return nil, fmt.Errorf("gopenjpeg: read header: %w", err)
	}
	if len(o.comps) > 0 {
		if err := container.SetDecodedComponents(o.comps, mgr); err != nil {
			return nil, fmt.Errorf("gopenjpeg: set components: %w", err)
		}
	}
	if o.tile >= 0 {
		if err := container.GetTile(stream, img, uint32(o.tile), mgr); err != nil {
			return nil, fmt.Errorf("gopenjpeg: decode tile: %w", err)
		}
		return newImage(img), nil
	}
	var x0, y0, x1, y1 int32
	if o.area != nil {
		x0, y0, x1, y1 = o.area[0], o.area[1], o.area[2], o.area[3]
	}
	if err := container.SetDecodeArea(img, x0, y0, x1, y1, mgr); err != nil {
		return nil, fmt.Errorf("gopenjpeg: set decode area: %w", err)
	}
	if err := container.Decode(stream, img, mgr); err != nil {
		return nil, fmt.Errorf("gopenjpeg: decode: %w", err)
	}
	if err := container.EndDecompress(stream, mgr); err != nil {
		return nil, fmt.Errorf("gopenjpeg: end decompress: %w", err)
	}
	return newImage(img), nil
}
