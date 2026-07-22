// Encode-side port of j2k.c (owned by W9): opj_j2k_setup_encoder plus the
// start_compress / encode / end_compress state machine and every marker writer
// (SOC SIZ COD COC QCD QCC POC RGN COM TLM SOT SOD EOC). Byte-identity with
// opj_compress is the goal, so integer widths and float math order match C.
//
// The library never panics: all failures return an error.

package j2k

import (
	"errors"
	"math"

	_ "github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/dwt"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
	_ "github.com/mgilbir/gopenjpeg/internal/mct"
	"github.com/mgilbir/gopenjpeg/internal/opjmath"
	_ "github.com/mgilbir/gopenjpeg/internal/pi"
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

	TcpMct int32 // 0=none, 1=RCT/ICT, 2=custom (custom unsupported here)

	Numpocs uint32
	POC     [cparams.MaxPocs]POCParam

	RoiCompno int32
	RoiShift  int32

	ResSpec  int32
	PrcwInit [cparams.MaxRLvls]int32
	PrchInit [cparams.MaxRLvls]int32
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
}

// CreateCompress ports opj_j2k_create_compress.
func CreateCompress() *Encoder {
	e := &Encoder{}
	e.CP.MIsDecoder = false
	return e
}

// intPow2 returns 1<<n as int32.
func intPow2(n int32) int32 { return int32(1) << uint(n) }

// SetupEncoder ports opj_j2k_setup_encoder.
func (e *Encoder) SetupEncoder(parameters *CParameters, img *image.Image, mgr *event.Manager) error {
	if parameters == nil || img == nil {
		return ErrEncodeSetup
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
		for i := int32(0); i < parameters.TcpNumlayers; i++ {
			if parameters.TcpRates[i] < tempRate {
				parameters.TcpRates[i] = tempRate
			}
		}
	}

	if cparams.IsCinema(parameters.Rsiz) || cparams.IsIMF(parameters.Rsiz) {
		e.enc.tlm = true
	}
	// NOTE: cinema/IMF profile coercion (opj_j2k_set_cinema_parameters /
	// is_*_compliant) is not ported here; the encode gate does not exercise it.

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

	if parameters.CpComment != nil {
		cp.Comment = *parameters.CpComment
	} else {
		cp.Comment = "Created by OpenJPEG version " + OpenJPEGVersion
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
		if cp.Tw > 65535/cp.Th {
			mgr.Errorf("Invalid number of tiles : %u x %u (maximum fixed by jpeg2000 norm is 65535 tiles)\n",
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

		// Custom MCT (mct==2 / mct_data) path is not ported; standard RCT/ICT only.
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
