// Encode-side port of j2k.c (owned by W9): opj_j2k_setup_encoder plus the
// start_compress / encode / end_compress state machine and every marker writer
// (SOC SIZ COD COC QCD QCC POC RGN COM TLM SOT SOD EOC). Byte-identity with
// opj_compress is the goal, so integer widths and float math order match C.
//
// The library never panics: all failures return an error.

package j2k

import (
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/dwt"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	"github.com/mgilbir/gopenjpeg/internal/tcd"
)

// OpenJPEGVersion is the version string embedded in the default COM comment,
// matching opj_version() of the oracle build (byte-identity depends on it).
const OpenJPEGVersion = "2.5.4"

// Errors returned by the encoder.
var (
	ErrEncodeSetup = errors.New("j2k: invalid encoder parameters")
	ErrEncodeWrite = errors.New("j2k: codestream write failed")
	ErrEncodeTile  = errors.New("j2k: tile encoding failed")
)

// POCParam ports the opj_poc_t fields the encoder reads from user parameters.
type POCParam struct {
	Tile    uint32
	Resno0  uint32
	Compno0 uint32
	Layno1  uint32
	Resno1  uint32
	Compno1 uint32
	Prg1    cparams.ProgOrder
}

// CParameters is the Go equivalent of opj_cparameters_t (the subset of fields
// that affect the codestream). Field names follow the C members.
type CParameters struct {
	Rsiz          uint16
	NumResolution int32
	CblockWInit   int32
	CblockHInit   int32
	ProgOrder     cparams.ProgOrder
	Csty          int32
	Mode          int32 // cblksty (-M mode switches)
	Irreversible  int32

	TcpNumlayers   int32
	TcpRates       [100]float32
	TcpDistoratio  [100]float32
	CpDistoAlloc   int32
	CpFixedQuality int32
	CpFixedAlloc   int32
	CpMatrice      []int32

	MaxCompSize int32
	MaxCsSize   int32

	CpTdx      int32
	CpTdy      int32
	CpTx0      int32
	CpTy0      int32
	TileSizeOn bool

	TpOn   int32
	TpFlag byte

	// CpComment nil selects the default "Created by OpenJPEG version X" comment.
	CpComment *string

	TcpMct int32 // 0=none, 1=RCT/ICT, 2=custom (mct_data required)

	// MctData holds the custom MCT coding matrix (numcomps*numcomps float32),
	// non-nil only when TcpMct==2. Ports the matrix part of
	// opj_cparameters_t.mct_data (set via opj_set_MCT).
	MctData []float32
	// MctDcShift holds the numcomps DC shifts that follow the matrix in the C
	// mct_data buffer.
	MctDcShift []int32

	Numpocs uint32
	POC     [cparams.MaxPocs]POCParam

	RoiCompno int32
	RoiShift  int32

	ResSpec  int32
	PrcwInit [cparams.MaxRLvls]int32
	PrchInit [cparams.MaxRLvls]int32

	// ImageOffsetX0/Y0 port image_offset_x0/y0 (set by cinema/IMF coercion).
	ImageOffsetX0 int32
	ImageOffsetY0 int32
	SubsamplingDx int32
	SubsamplingDy int32
}

// encoderState ports the opj_j2k_enc_t fields the write path uses.
type encoderState struct {
	currentPocTilePartNumber uint32 // m_current_poc_tile_part_number (tp_num)
	currentTilePartNumber    uint32 // m_current_tile_part_number (cur_tp_num)

	tlm         bool  // m_TLM
	ttlmiIsByte bool  // m_Ttlmi_is_byte
	tlmStart    int64 // m_tlm_start

	tlmBuffer  []byte // m_tlm_sot_offsets_buffer
	tlmCurrent int    // index into tlmBuffer (m_tlm_sot_offsets_current)

	totalTileParts uint32 // m_total_tile_parts

	encodedTileData []byte // m_encoded_tile_data
	encodedTileSize uint32 // m_encoded_tile_size

	plt              bool   // m_PLT
	reservedBytesPLT uint32 // m_reserved_bytes_for_PLT

	nbComps uint32 // m_nb_comps
}

// Encoder is the exported J2K compressor, mirroring the encode API surface of
// the C opj_j2k_t. It shares the coding-parameter struct (CP) with the decoder.
type Encoder struct {
	CP           cparams.CP
	privateImage *image.Image
	enc          encoderState

	currentTileNumber uint32
	tcd               *tcd.TCD

	// numThreads ports the encode-side worker count (opj_j2k_set_threads),
	// applied to the tile coder for per-code-block tier-1 encode parallelism.
	numThreads int
}

// CreateCompress ports opj_j2k_create_compress.
func CreateCompress() *Encoder {
	e := &Encoder{}
	e.CP.MIsDecoder = false
	return e
}

// SetThreads records the worker count for the parallelizable encode stages
// (per-code-block tier-1). n<=1 keeps the single-threaded path. Mirrors
// opj_j2k_set_threads on the compressor.
func (e *Encoder) SetThreads(n int) { e.numThreads = n }

// intPow2 returns 1<<n as int32.
func intPow2(n int32) int32 { return int32(1) << uint(n) }

// validateEncodeInputs rejects the parameter/image combinations that OpenJPEG
// caught at its CLI layer but that this port would otherwise carry into the
// trusting internals — where a count exceeding a fixed-size array, a degenerate
// tile geometry, or a malformed component would panic or emit a corrupt
// codestream. Every check returns an error wrapping ErrEncodeSetup (so callers
// can test errors.Is) and, where a manager is installed, mirrors C's diagnostic.
func validateEncodeInputs(parameters *CParameters, img *image.Image, mgr *event.Manager) error {
	if err := img.ValidateForEncode(); err != nil {
		mgr.Errorf("%s\n", err.Error())
		return fmt.Errorf("%w: %v", ErrEncodeSetup, err)
	}

	// Layer count: TcpRates/TcpDistoratio are fixed [100] arrays indexed by
	// TcpNumlayers for every allocation strategy (C1, C30). 0 is a valid
	// sentinel meaning "default to one lossless layer" (handled below).
	if parameters.TcpNumlayers < 0 || int(parameters.TcpNumlayers) > len(parameters.TcpRates) {
		mgr.Errorf("Invalid number of quality layers: %d not in range [0,%d]\n",
			parameters.TcpNumlayers, len(parameters.TcpRates))
		return fmt.Errorf("%w: number of quality layers %d not in range [0,%d]",
			ErrEncodeSetup, parameters.TcpNumlayers, len(parameters.TcpRates))
	}

	// POC count: POC is a fixed [MaxPocs] array (C2).
	if parameters.Numpocs > cparams.MaxPocs {
		mgr.Errorf("Invalid number of progression order changes: %d exceeds %d\n",
			parameters.Numpocs, cparams.MaxPocs)
		return fmt.Errorf("%w: number of progression order changes %d exceeds %d",
			ErrEncodeSetup, parameters.Numpocs, cparams.MaxPocs)
	}

	// Custom precincts: PrcwInit[ResSpec-1] is read when PRT styling is on, so a
	// zero ResSpec underflows the index (C3).
	if uint32(parameters.Csty)&cparams.CPCstyPRT != 0 &&
		(parameters.ResSpec < 1 || parameters.ResSpec > cparams.MaxRLvls) {
		mgr.Errorf("Invalid number of precinct resolutions: %d not in range [1,%d]\n",
			parameters.ResSpec, cparams.MaxRLvls)
		return fmt.Errorf("%w: number of precinct resolutions %d not in range [1,%d]",
			ErrEncodeSetup, parameters.ResSpec, cparams.MaxRLvls)
	}

	// Tile geometry: negative tile sizes/origins wrap to huge uint32 and encode
	// silently; a zero tile size divides by zero (C44, C5).
	if parameters.TileSizeOn && (parameters.CpTdx <= 0 || parameters.CpTdy <= 0) {
		mgr.Errorf("Invalid tile size: %d x %d (must be positive)\n",
			parameters.CpTdx, parameters.CpTdy)
		return fmt.Errorf("%w: tile size %d x %d must be positive",
			ErrEncodeSetup, parameters.CpTdx, parameters.CpTdy)
	}
	if parameters.CpTx0 < 0 || parameters.CpTy0 < 0 {
		mgr.Errorf("Invalid tile origin: %d,%d (must be non-negative)\n",
			parameters.CpTx0, parameters.CpTy0)
		return fmt.Errorf("%w: tile origin %d,%d must be non-negative",
			ErrEncodeSetup, parameters.CpTx0, parameters.CpTy0)
	}

	// Multi-component transform applicability. opj_tcd_mct_encode reads every
	// component with COMPONENT 0's sample count and, for mct==1, dereferences
	// comps[1]/comps[2] unconditionally — undefined behaviour in C for a
	// sub-sampled or short component array, an index-out-of-range panic here.
	// OpenJPEG caught both at its CLI (opj_compress.c: "RGB->YCC conversion
	// cannot be used"), which this port replaced with options that cannot fail;
	// so the check belongs at this choke point (T1).
	if parameters.TcpMct == 1 && img.Numcomps < 3 {
		mgr.Errorf("RGB->YCC conversion cannot be used:\nInput image has less than 3 components\n")
		return fmt.Errorf("%w: MCT requires at least 3 components, image has %d",
			ErrEncodeSetup, img.Numcomps)
	}
	if parameters.TcpMct == 2 {
		if len(parameters.MctData) == 0 {
			mgr.Errorf("Custom MCT has been set but no array-based MCT has been provided.\n")
			return fmt.Errorf("%w: custom MCT selected without a coding matrix", ErrEncodeSetup)
		}
		// The custom transform walks all components with comp 0's sample count,
		// so they must all have the same geometry (the mct==1 path warns and
		// disables instead; a custom matrix cannot be silently dropped, since
		// the MCC/MCT marker records are already sized from it).
		for i := uint32(1); i < img.Numcomps; i++ {
			if img.Comps[i].Dx != img.Comps[0].Dx || img.Comps[i].Dy != img.Comps[0].Dy {
				mgr.Errorf("Cannot perform custom MCT on components with different sizes.\n")
				return fmt.Errorf("%w: custom MCT requires equally sized components "+
					"(component %d has dx=%d dy=%d, component 0 has dx=%d dy=%d)",
					ErrEncodeSetup, i, img.Comps[i].Dx, img.Comps[i].Dy,
					img.Comps[0].Dx, img.Comps[0].Dy)
			}
		}
	}

	// Tile-part grouping. opj_compress accepts only R, L or C for -TP, so any
	// other byte is outside the envelope C ever encodes: get_num_tp then never
	// matches the progression string, the tile-part count degenerates to the
	// full packet product with tp_pos left unset, and the packet iterator
	// re-visits packets until cblk->numpasses walks past the passes array (a
	// heap over-read in C, a panic here).
	if parameters.TpOn != 0 {
		switch parameters.TpFlag {
		case 'R', 'L', 'C':
		default:
			mgr.Errorf("Invalid tile-part grouping %q: must be R, L or C\n", parameters.TpFlag)
			return fmt.Errorf("%w: invalid tile-part grouping %q (want 'R', 'L' or 'C')",
				ErrEncodeSetup, parameters.TpFlag)
		}
	}

	// Progression order: opj_j2k_convert_progression_order returns the empty
	// sentinel string for an unrecognised order, and the tile-part / POC
	// iterators then index it positionally (pi.CreateEncode walks prog[0..3]).
	// C reads past the terminator of a static ""; Go panics on the empty string.
	// Both the tile's order and every POC record's order have to be real.
	if cparams.ConvertProgressionOrder(parameters.ProgOrder) == "" {
		mgr.Errorf("Invalid progression order: %d\n", parameters.ProgOrder)
		return fmt.Errorf("%w: invalid progression order %d",
			ErrEncodeSetup, parameters.ProgOrder)
	}
	for i := uint32(0); i < parameters.Numpocs && int(i) < len(parameters.POC); i++ {
		if cparams.ConvertProgressionOrder(parameters.POC[i].Prg1) == "" {
			mgr.Errorf("Invalid progression order in POC %d: %d\n", i, parameters.POC[i].Prg1)
			return fmt.Errorf("%w: invalid progression order %d in POC record %d",
				ErrEncodeSetup, parameters.POC[i].Prg1, i)
		}
		// A progression-order change whose end bounds are zero describes an
		// empty progression volume. opj_j2k_get_num_tp multiplies those bounds
		// into the tile-part count, so the count comes out 0 while the encoder
		// still writes a tile part — in C that overflows the TLM offsets buffer
		// (opj_malloc(0)); here it panicked. opj_compress's -POC syntax cannot
		// express it; reject it explicitly.
		poc := &parameters.POC[i]
		if poc.Layno1 == 0 || poc.Resno1 == 0 || poc.Compno1 == 0 {
			mgr.Errorf("Invalid POC %d: layer/resolution/component end must be non-zero "+
				"(got layno1=%d resno1=%d compno1=%d)\n", i, poc.Layno1, poc.Resno1, poc.Compno1)
			return fmt.Errorf("%w: POC record %d has an empty progression volume "+
				"(layno1=%d resno1=%d compno1=%d)",
				ErrEncodeSetup, i, poc.Layno1, poc.Resno1, poc.Compno1)
		}
	}

	return nil
}

// warnLayerOrdering ports the two diagnostic loops of opj_j2k_setup_encoder that
// follow the "no explicit layers" defaulting: rate allocation wants strictly
// decreasing tcp_rates and fixed-quality allocation wants strictly increasing
// tcp_distoratio. Both are message-only — C never alters the parameters here, so
// neither does this port (C29).
func warnLayerOrdering(parameters *CParameters, mgr *event.Manager) {
	numlayers := parameters.TcpNumlayers
	switch {
	case parameters.CpDistoAlloc != 0:
		for i := int32(1); i < numlayers; i++ {
			// C clamps each rate to >= 1.0 before comparing, because a rate of
			// 1 or less means lossless; the four message variants report which
			// of the two operands was corrected.
			rateI := parameters.TcpRates[i]
			ratePrev := parameters.TcpRates[i-1]
			rateICorr := rateI
			if rateICorr <= 1.0 {
				rateICorr = 1.0
			}
			ratePrevCorr := ratePrev
			if ratePrevCorr <= 1.0 {
				ratePrevCorr = 1.0
			}
			if rateICorr < ratePrevCorr {
				continue
			}
			switch {
			case rateICorr != rateI && ratePrevCorr != ratePrev:
				mgr.Warnf("tcp_rates[%d]=%f (corrected as %f) should be strictly lesser "+
					"than tcp_rates[%d]=%f (corrected as %f)\n",
					i, rateI, rateICorr, i-1, ratePrev, ratePrevCorr)
			case rateICorr != rateI:
				mgr.Warnf("tcp_rates[%d]=%f (corrected as %f) should be strictly lesser "+
					"than tcp_rates[%d]=%f\n", i, rateI, rateICorr, i-1, ratePrev)
			case ratePrevCorr != ratePrev:
				mgr.Warnf("tcp_rates[%d]=%f should be strictly lesser "+
					"than tcp_rates[%d]=%f (corrected as %f)\n",
					i, rateI, i-1, ratePrev, ratePrevCorr)
			default:
				mgr.Warnf("tcp_rates[%d]=%f should be strictly lesser "+
					"than tcp_rates[%d]=%f\n", i, rateI, i-1, ratePrev)
			}
		}
	case parameters.CpFixedQuality != 0:
		for i := int32(1); i < numlayers; i++ {
			// A trailing 0 distoratio is the "lossless final layer" idiom and is
			// exempt.
			if parameters.TcpDistoratio[i] < parameters.TcpDistoratio[i-1] &&
				!(i == numlayers-1 && parameters.TcpDistoratio[i] == 0) {
				mgr.Warnf("tcp_distoratio[%d]=%f should be strictly greater "+
					"than tcp_distoratio[%d]=%f\n",
					i, parameters.TcpDistoratio[i], i-1, parameters.TcpDistoratio[i-1])
			}
		}
	}
}

// SetupEncoder ports opj_j2k_setup_encoder.
func (e *Encoder) SetupEncoder(parameters *CParameters, img *image.Image, mgr *event.Manager) error {
	if parameters == nil || img == nil {
		return ErrEncodeSetup
	}

	// Single validation choke point. OpenJPEG relied on opj_compress's CLI layer
	// to reject out-of-range counts and degenerate geometry before they reached
	// the trusting internals; this port dropped that layer, so validate the
	// assembled parameters and image here (covers both the J2K and JP2 paths,
	// which both funnel through SetupEncoder) instead of panicking downstream.
	if err := validateEncodeInputs(parameters, img, mgr); err != nil {
		return err
	}

	if parameters.NumResolution <= 0 || parameters.NumResolution > cparams.MaxRLvls {
		mgr.Errorf("Invalid number of resolutions : %d not in range [1,%d]\n",
			parameters.NumResolution, cparams.MaxRLvls)
		return ErrEncodeSetup
	}
	if parameters.CblockWInit < 4 || parameters.CblockWInit > 1024 {
		mgr.Errorf("Invalid value for cblockw_init: %d not a power of 2 in range [4,1024]\n",
			parameters.CblockWInit)
		return ErrEncodeSetup
	}
	if parameters.CblockHInit < 4 || parameters.CblockHInit > 1024 {
		mgr.Errorf("Invalid value for cblockh_init: %d not a power of 2 not in range [4,1024]\n",
			parameters.CblockHInit)
		return ErrEncodeSetup
	}
	if parameters.CblockWInit*parameters.CblockHInit > 4096 {
		mgr.Errorf("Invalid value for cblockw_init * cblockh_init: should be <= 4096\n")
		return ErrEncodeSetup
	}
	cblkw := opjmath.IntFloorlog2(parameters.CblockWInit)
	cblkh := opjmath.IntFloorlog2(parameters.CblockHInit)
	if parameters.CblockWInit != intPow2(cblkw) {
		mgr.Errorf("Invalid value for cblockw_init: %d not a power of 2 in range [4,1024]\n",
			parameters.CblockWInit)
		return ErrEncodeSetup
	}
	if parameters.CblockHInit != intPow2(cblkh) {
		mgr.Errorf("Invalid value for cblockw_init: %d not a power of 2 in range [4,1024]\n",
			parameters.CblockHInit)
		return ErrEncodeSetup
	}

	if parameters.CpFixedAlloc != 0 {
		if parameters.CpMatrice == nil {
			mgr.Errorf("cp_fixed_alloc set, but cp_matrice missing\n")
			return ErrEncodeSetup
		}
		if parameters.TcpNumlayers > cparams.TCDMatrixMaxLayerCount {
			mgr.Errorf("tcp_numlayers when cp_fixed_alloc set should not exceed %d\n",
				cparams.TCDMatrixMaxLayerCount)
			return ErrEncodeSetup
		}
		if parameters.NumResolution > cparams.TCDMatrixMaxResolutionCount {
			mgr.Errorf("numresolution when cp_fixed_alloc set should not exceed %d\n",
				cparams.TCDMatrixMaxResolutionCount)
			return ErrEncodeSetup
		}
	}

	e.enc.nbComps = img.Numcomps
	cp := &e.CP
	cp.Tw = 1
	cp.Th = 1

	// If no explicit layers are provided, use lossless settings.
	if parameters.TcpNumlayers == 0 {
		parameters.TcpNumlayers = 1
		parameters.CpDistoAlloc = 1
		parameters.TcpRates[0] = 0
	}

	// Diagnostics only (they never change the codestream): C warns when the
	// requested layer rates are not strictly decreasing, or when the requested
	// fixed-quality distortion ratios are not strictly increasing (C29).
	warnLayerOrdering(parameters, mgr)

	// see if max_codestream_size does limit input rate
	if parameters.MaxCsSize <= 0 {
		if parameters.TcpRates[parameters.TcpNumlayers-1] > 0 {
			tempSize := float32((float64(img.Numcomps) * float64(img.Comps[0].W) *
				float64(img.Comps[0].H) * float64(img.Comps[0].Prec)) /
				(float64(parameters.TcpRates[parameters.TcpNumlayers-1]) * 8 *
					float64(img.Comps[0].Dx) * float64(img.Comps[0].Dy)))
			if tempSize > float32(math.MaxInt32) {
				parameters.MaxCsSize = math.MaxInt32
			} else {
				parameters.MaxCsSize = int32(math.Floor(float64(tempSize)))
			}
		} else {
			parameters.MaxCsSize = 0
		}
	} else {
		if cparams.IsIMF(parameters.Rsiz) && parameters.MaxCsSize > 0 &&
			parameters.TcpNumlayers == 1 && parameters.TcpRates[0] == 0 {
			parameters.TcpRates[0] = float32(float64(img.Numcomps)*float64(img.Comps[0].W)*
				float64(img.Comps[0].H)*float64(img.Comps[0].Prec)) /
				float32(uint32(parameters.MaxCsSize)*8*img.Comps[0].Dx*img.Comps[0].Dy)
		}
		tempRate := float32((float64(img.Numcomps) * float64(img.Comps[0].W) *
			float64(img.Comps[0].H) * float64(img.Comps[0].Prec)) /
			(float64(parameters.MaxCsSize) * 8 * float64(img.Comps[0].Dx) *
				float64(img.Comps[0].Dy)))
		capped := false
		for i := int32(0); i < parameters.TcpNumlayers; i++ {
			if parameters.TcpRates[i] < tempRate {
				parameters.TcpRates[i] = tempRate
				capped = true
			}
		}
		if capped {
			mgr.Warnf("The desired maximum codestream size has limited\n" +
				"at least one of the desired quality layers\n")
		}
	}

	if cparams.IsCinema(parameters.Rsiz) || cparams.IsIMF(parameters.Rsiz) {
		e.enc.tlm = true
	}

	// Manage profiles/applications and coerce parameters. Ports the
	// OPJ_IS_CINEMA / OPJ_IS_IMF / OPJ_IS_PART2 dispatch of opj_j2k_setup_encoder.
	switch {
	case cparams.IsCinema(parameters.Rsiz):
		if parameters.Rsiz == cparams.ProfileCinemaS2K || parameters.Rsiz == cparams.ProfileCinemaS4K {
			mgr.Warnf("JPEG 2000 Scalable Digital Cinema profiles not yet supported\n")
			parameters.Rsiz = cparams.ProfileNone
		} else {
			setCinemaParameters(parameters, img, mgr)
			if !isCinemaCompliant(img, parameters.Rsiz, mgr) {
				parameters.Rsiz = cparams.ProfileNone
			}
		}
	case cparams.IsStorage(parameters.Rsiz):
		// C cannot encode the long-term-storage profile, so it falls back to
		// PROFILE_NONE instead of writing the unsupported Rsiz into SIZ (C26).
		mgr.Warnf("JPEG 2000 Long Term Storage profile not yet supported\n")
		parameters.Rsiz = cparams.ProfileNone
	case cparams.IsBroadcast(parameters.Rsiz):
		mgr.Warnf("JPEG 2000 Broadcast profiles not yet supported\n")
		parameters.Rsiz = cparams.ProfileNone
	case cparams.IsIMF(parameters.Rsiz):
		setIMFParameters(parameters, img, mgr)
		if !isIMFCompliant(parameters, img, mgr) {
			parameters.Rsiz = cparams.ProfileNone
		}
	case cparams.IsPart2(parameters.Rsiz):
		if parameters.Rsiz == uint16(cparams.ProfilePart2|cparams.ExtensionNone) {
			mgr.Warnf("JPEG 2000 Part-2 profile defined\nbut no Part-2 extension enabled.\nProfile set to NONE.\n")
			parameters.Rsiz = cparams.ProfileNone
		} else if parameters.Rsiz != uint16(cparams.ProfilePart2|cparams.ExtensionMCT) {
			mgr.Warnf("Unsupported Part-2 extension enabled\nProfile set to NONE.\n")
			parameters.Rsiz = cparams.ProfileNone
		}
	}

	// copy user encoding parameters
	cp.MEnc.MMaxCompSize = uint32(parameters.MaxCompSize)
	cp.Rsiz = parameters.Rsiz
	switch {
	case parameters.CpFixedAlloc != 0:
		cp.MEnc.MQualityLayerAllocStrategy = cparams.FixedLayer
	case parameters.CpFixedQuality != 0:
		cp.MEnc.MQualityLayerAllocStrategy = cparams.FixedDistortionRatio
	default:
		cp.MEnc.MQualityLayerAllocStrategy = cparams.RateDistortionRatio
	}

	if parameters.CpFixedAlloc != 0 {
		n := int(parameters.TcpNumlayers) * int(parameters.NumResolution) * 3
		cp.MEnc.MMatrice = make([]int32, n)
		copy(cp.MEnc.MMatrice, parameters.CpMatrice[:n])
	}

	cp.Tdx = uint32(parameters.CpTdx)
	cp.Tdy = uint32(parameters.CpTdy)
	cp.Tx0 = uint32(parameters.CpTx0)
	cp.Ty0 = uint32(parameters.CpTy0)

	// C's cp->comment is either NULL (no COM marker) or a non-NULL string that
	// may be empty; opj_compress -C "" therefore writes an empty COM. Mirror
	// that by keeping the pointer (C53).
	if parameters.CpComment != nil {
		comment := *parameters.CpComment
		cp.Comment = &comment
	} else {
		comment := "Created by OpenJPEG version " + OpenJPEGVersion
		cp.Comment = &comment
	}

	// The tiling origin must lie inside the image. Past it, img.X1-cp.Tx0
	// underflows in uint32: with an explicit tile size that yields an absurd
	// tile count (caught below), but WITHOUT one it becomes the tile width
	// itself (~4e9), and the tile-component buffers are then sized from it —
	// a 4 GiB allocation reachable from two option calls. C computes the same
	// garbage; bound it here, at the single encode validation choke point.
	if cp.Tx0 >= img.X1 || cp.Ty0 >= img.Y1 {
		mgr.Errorf("Invalid tile origin: %d,%d is outside the image (%d,%d)-(%d,%d)\n",
			cp.Tx0, cp.Ty0, img.X0, img.Y0, img.X1, img.Y1)
		return ErrEncodeSetup
	}

	if parameters.TileSizeOn {
		if cp.Tdx == 0 {
			mgr.Errorf("Invalid tile width\n")
			return ErrEncodeSetup
		}
		if cp.Tdy == 0 {
			mgr.Errorf("Invalid tile height\n")
			return ErrEncodeSetup
		}
		cp.Tw = opjmath.UintCeildiv(img.X1-cp.Tx0, cp.Tdx)
		cp.Th = opjmath.UintCeildiv(img.Y1-cp.Ty0, cp.Tdy)
		// A tile origin at or beyond the image extent yields a zero tile count,
		// which then divides by zero in the 65535 check below and leaves Tcps
		// empty for every later stage (C5).
		if cp.Tw == 0 || cp.Th == 0 {
			mgr.Errorf("Invalid number of tiles : %d x %d (tile grid does not cover the image)\n",
				cp.Tw, cp.Th)
			return ErrEncodeSetup
		}
		if cp.Tw > 65535/cp.Th {
			mgr.Errorf("Invalid number of tiles : %d x %d (maximum fixed by jpeg2000 norm is 65535 tiles)\n",
				cp.Tw, cp.Th)
			return ErrEncodeSetup
		}
	} else {
		cp.Tdx = img.X1 - cp.Tx0
		cp.Tdy = img.Y1 - cp.Ty0
	}

	if parameters.TpOn != 0 {
		cp.MEnc.MTpFlag = parameters.TpFlag
		cp.MEnc.MTpOn = 1
	}

	nbTiles := cp.Tw * cp.Th
	cp.Tcps = make([]cparams.TCP, nbTiles)

	for tileno := uint32(0); tileno < nbTiles; tileno++ {
		tcp := &cp.Tcps[tileno]
		fixedDistoratio := cp.MEnc.MQualityLayerAllocStrategy == cparams.FixedDistortionRatio
		tcp.Numlayers = uint32(parameters.TcpNumlayers)

		for j := uint32(0); j < tcp.Numlayers; j++ {
			if cparams.IsCinema(cp.Rsiz) || cparams.IsIMF(cp.Rsiz) {
				if fixedDistoratio {
					tcp.Distoratio[j] = parameters.TcpDistoratio[j]
				}
				tcp.Rates[j] = parameters.TcpRates[j]
			} else {
				if fixedDistoratio {
					tcp.Distoratio[j] = parameters.TcpDistoratio[j]
				} else {
					tcp.Rates[j] = parameters.TcpRates[j]
				}
			}
			if !fixedDistoratio && tcp.Rates[j] <= 1.0 {
				tcp.Rates[j] = 0.0 // force lossless
			}
		}

		tcp.Csty = uint32(parameters.Csty)
		tcp.Prg = parameters.ProgOrder
		tcp.MCT = uint32(parameters.TcpMct)

		numpocsTile := uint32(0)
		tcp.POC = 0
		if parameters.Numpocs != 0 {
			for i := uint32(0); i < parameters.Numpocs; i++ {
				if tileno+1 == parameters.POC[i].Tile {
					if parameters.POC[numpocsTile].Compno0 >= img.Numcomps {
						mgr.Errorf("Invalid compno0 for POC %d\n", i)
						return ErrEncodeSetup
					}
					tcpPoc := &tcp.Pocs[numpocsTile]
					tcpPoc.Resno0 = parameters.POC[numpocsTile].Resno0
					tcpPoc.Compno0 = parameters.POC[numpocsTile].Compno0
					tcpPoc.Layno1 = parameters.POC[numpocsTile].Layno1
					tcpPoc.Resno1 = parameters.POC[numpocsTile].Resno1
					tcpPoc.Compno1 = opjmath.UintMin(parameters.POC[numpocsTile].Compno1, img.Numcomps)
					tcpPoc.Prg1 = parameters.POC[numpocsTile].Prg1
					tcpPoc.Tile = parameters.POC[numpocsTile].Tile
					numpocsTile++
				}
			}
			if numpocsTile != 0 {
				checkPocVal(parameters.POC[:], tileno, parameters.Numpocs,
					uint32(parameters.NumResolution), img.Numcomps,
					uint32(parameters.TcpNumlayers), mgr)
				tcp.POC = 1
				tcp.Numpocs = numpocsTile - 1
			}
		} else {
			tcp.Numpocs = 0
		}

		tcp.TCCPs = make([]cparams.TCCP, img.Numcomps)

		if parameters.MctData != nil {
			// Custom (Part-2 array-based) MCT: port of the mct_data branch of
			// opj_j2k_setup_encoder.
			if err := e.setupCustomMCT(tcp, parameters, img, mgr); err != nil {
				return err
			}
		} else {
			if tcp.MCT == 1 && img.Numcomps >= 3 {
				if img.Comps[0].Dx != img.Comps[1].Dx || img.Comps[0].Dx != img.Comps[2].Dx ||
					img.Comps[0].Dy != img.Comps[1].Dy || img.Comps[0].Dy != img.Comps[2].Dy {
					mgr.Warnf("Cannot perform MCT on components with different sizes. Disabling MCT.\n")
					tcp.MCT = 0
				}
			}
			for i := uint32(0); i < img.Numcomps; i++ {
				tccp := &tcp.TCCPs[i]
				if img.Comps[i].Sgnd == 0 {
					tccp.MDcLevelShift = int32(uint32(1) << (img.Comps[i].Prec - 1))
				}
			}
		}

		for i := uint32(0); i < img.Numcomps; i++ {
			tccp := &tcp.TCCPs[i]
			tccp.Csty = uint32(parameters.Csty) & 0x01
			tccp.Numresolutions = uint32(parameters.NumResolution)
			tccp.Cblkw = uint32(opjmath.IntFloorlog2(parameters.CblockWInit))
			tccp.Cblkh = uint32(opjmath.IntFloorlog2(parameters.CblockHInit))
			tccp.Cblksty = uint32(parameters.Mode)
			if parameters.Irreversible != 0 {
				tccp.Qmfbid = 0
				tccp.Qntsty = cparams.CCPQntStySeQnt
			} else {
				tccp.Qmfbid = 1
				tccp.Qntsty = cparams.CCPQntStyNoQnt
			}
			if cparams.IsCinema(parameters.Rsiz) && parameters.Rsiz == cparams.ProfileCinema2K {
				tccp.Numgbits = 1
			} else {
				tccp.Numgbits = 2
			}
			if int32(i) == parameters.RoiCompno {
				tccp.Roishift = parameters.RoiShift
			} else {
				tccp.Roishift = 0
			}

			if uint32(parameters.Csty)&cparams.CPCstyPRT != 0 {
				p := int32(0)
				for itRes := int32(tccp.Numresolutions) - 1; itRes >= 0; itRes-- {
					if p < parameters.ResSpec {
						if parameters.PrcwInit[p] < 1 {
							tccp.Prcw[itRes] = 1
						} else {
							tccp.Prcw[itRes] = uint32(opjmath.IntFloorlog2(parameters.PrcwInit[p]))
						}
						if parameters.PrchInit[p] < 1 {
							tccp.Prch[itRes] = 1
						} else {
							tccp.Prch[itRes] = uint32(opjmath.IntFloorlog2(parameters.PrchInit[p]))
						}
					} else {
						resSpec := parameters.ResSpec
						sizePrcw := parameters.PrcwInit[resSpec-1] >> uint(p-(resSpec-1))
						sizePrch := parameters.PrchInit[resSpec-1] >> uint(p-(resSpec-1))
						if sizePrcw < 1 {
							tccp.Prcw[itRes] = 1
						} else {
							tccp.Prcw[itRes] = uint32(opjmath.IntFloorlog2(sizePrcw))
						}
						if sizePrch < 1 {
							tccp.Prch[itRes] = 1
						} else {
							tccp.Prch[itRes] = uint32(opjmath.IntFloorlog2(sizePrch))
						}
					}
					p++
				}
			} else {
				for j := uint32(0); j < tccp.Numresolutions; j++ {
					tccp.Prcw[j] = 15
					tccp.Prch[j] = 15
				}
			}

			calcExplicitStepsizes(tccp, img.Comps[i].Prec)
		}
	}

	return nil
}

// calcExplicitStepsizes bridges cparams.TCCP to dwt.CalcExplicitStepsizes.
func calcExplicitStepsizes(tccp *cparams.TCCP, prec uint32) {
	numbands := 3*tccp.Numresolutions - 2
	dt := &dwt.Tccp{
		Numresolutions: tccp.Numresolutions,
		Qmfbid:         tccp.Qmfbid,
		Qntsty:         tccp.Qntsty,
		Stepsizes:      make([]dwt.Stepsize, numbands),
	}
	dwt.CalcExplicitStepsizes(dt, prec)
	for i := uint32(0); i < numbands; i++ {
		tccp.Stepsizes[i].Expn = dt.Stepsizes[i].Expn
		tccp.Stepsizes[i].Mant = dt.Stepsizes[i].Mant
	}
}

// checkPocVal ports opj_j2k_check_poc_val.
func checkPocVal(pocs []POCParam, tileno, nbPocs, nbResolutions, numComps, numLayers uint32,
	mgr *event.Manager) bool {
	stepC := uint32(1)
	stepR := numComps * stepC
	stepL := nbResolutions * stepR
	packetArray := make([]uint32, stepL*numLayers)
	loss := false

	for i := uint32(0); i < nbPocs; i++ {
		poc := &pocs[i]
		if tileno+1 == poc.Tile {
			index := stepR * poc.Resno0
			for resno := poc.Resno0; resno < opjmath.UintMin(poc.Resno1, nbResolutions); resno++ {
				resIndex := index + poc.Compno0*stepC
				for compno := poc.Compno0; compno < opjmath.UintMin(poc.Compno1, numComps); compno++ {
					compIndex := resIndex
					for layno := uint32(0); layno < opjmath.UintMin(poc.Layno1, numLayers); layno++ {
						if compIndex < uint32(len(packetArray)) {
							packetArray[compIndex] = 1
						}
						compIndex += stepL
					}
					resIndex += stepC
				}
				index += stepR
			}
		}
	}

	index := uint32(0)
	for layno := uint32(0); layno < numLayers; layno++ {
		for resno := uint32(0); resno < nbResolutions; resno++ {
			for compno := uint32(0); compno < numComps; compno++ {
				if index < uint32(len(packetArray)) && packetArray[index] != 1 {
					loss = true
				}
				index += stepC
			}
		}
	}
	if loss {
		mgr.Errorf("Missing packets possible loss of data\n")
	}
	return !loss
}
