package j2k

// Cinema and IMF profile parameter coercion and compliance checks, ports of
// opj_j2k_set_cinema_parameters / opj_j2k_is_cinema_compliant /
// opj_j2k_set_imf_parameters / opj_j2k_is_imf_compliant / opj_j2k_get_imf_max_NL
// / opj_j2k_initialise_4K_poc from j2k.c. Warning text and parameter mutations
// match the C reference so the coerced codestream is byte-identical.

import (
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/image"
)

// Cinema rate limits ported from openjpeg.h.
const (
	cinema24CS   = 1302083 // OPJ_CINEMA_24_CS
	cinema24Comp = 1041666 // OPJ_CINEMA_24_COMP
)

// Default encoder parameter sentinels ported from opj_common.h, used by the IMF
// override logic.
const (
	compParamDefaultCblockW       = 64
	compParamDefaultCblockH       = 64
	compParamDefaultNumResolution = 6
)

// IMF profile / level extraction macros ported from openjpeg.h.
func getIMFProfile(rsiz uint16) uint16   { return rsiz & 0xff00 }
func getIMFMainlevel(rsiz uint16) uint16 { return rsiz & 0xf }
func getIMFSublevel(rsiz uint16) uint16  { return (rsiz >> 4) & 0xf }

const imfMainlevelMax = 11

// IMF profile bases ported from openjpeg.h.
const (
	profileIMF2K  = 0x0400
	profileIMF4K  = 0x0500
	profileIMF8K  = 0x0600
	profileIMF2KR = 0x0700
	profileIMF4KR = 0x0800
	profileIMF8KR = 0x0900
)

// initialise4KPoc ports opj_j2k_initialise_4K_poc.
func initialise4KPoc(poc []POCParam, numres int32) uint32 {
	poc[0].Tile = 1
	poc[0].Resno0 = 0
	poc[0].Compno0 = 0
	poc[0].Layno1 = 1
	poc[0].Resno1 = uint32(numres - 1)
	poc[0].Compno1 = 3
	poc[0].Prg1 = cparams.CPRL
	poc[1].Tile = 1
	poc[1].Resno0 = uint32(numres - 1)
	poc[1].Compno0 = 0
	poc[1].Layno1 = 1
	poc[1].Resno1 = uint32(numres)
	poc[1].Compno1 = 3
	poc[1].Prg1 = cparams.CPRL
	return 2
}

// setCinemaParameters ports opj_j2k_set_cinema_parameters.
func setCinemaParameters(p *CParameters, img *image.Image, mgr *event.Manager) {
	p.TileSizeOn = false
	p.CpTdx = 1
	p.CpTdy = 1
	p.TpFlag = 'C'
	p.TpOn = 1
	p.CpTx0 = 0
	p.CpTy0 = 0
	p.ImageOffsetX0 = 0
	p.ImageOffsetY0 = 0
	p.CblockWInit = 32
	p.CblockHInit = 32
	p.Mode = 0
	p.RoiCompno = -1
	p.SubsamplingDx = 1
	p.SubsamplingDy = 1
	p.Irreversible = 1

	if p.TcpNumlayers > 1 {
		mgr.Warnf("JPEG 2000 Profile-3 and 4 (2k/4k dc profile) requires:\n"+
			"1 single quality layer"+
			"-> Number of layers forced to 1 (rather than %d)\n"+
			"-> Rate of the last layer (%3.1f) will be used",
			p.TcpNumlayers, p.TcpRates[p.TcpNumlayers-1])
		p.TcpRates[0] = p.TcpRates[p.TcpNumlayers-1]
		p.TcpNumlayers = 1
	}

	switch p.Rsiz {
	case cparams.ProfileCinema2K:
		if p.NumResolution > 6 {
			mgr.Warnf("JPEG 2000 Profile-3 (2k dc profile) requires:\n"+
				"Number of decomposition levels <= 5\n"+
				"-> Number of decomposition levels forced to 5 (rather than %d)\n",
				p.NumResolution+1)
			p.NumResolution = 6
		}
	case cparams.ProfileCinema4K:
		if p.NumResolution < 2 {
			mgr.Warnf("JPEG 2000 Profile-4 (4k dc profile) requires:\n"+
				"Number of decomposition levels >= 1 && <= 6\n"+
				"-> Number of decomposition levels forced to 1 (rather than %d)\n",
				p.NumResolution+1)
			p.NumResolution = 1
		} else if p.NumResolution > 7 {
			mgr.Warnf("JPEG 2000 Profile-4 (4k dc profile) requires:\n"+
				"Number of decomposition levels >= 1 && <= 6\n"+
				"-> Number of decomposition levels forced to 6 (rather than %d)\n",
				p.NumResolution+1)
			p.NumResolution = 7
		}
	}

	p.Csty |= cparams.CPCstyPRT
	if p.NumResolution == 1 {
		p.ResSpec = 1
		p.PrcwInit[0] = 128
		p.PrchInit[0] = 128
	} else {
		p.ResSpec = p.NumResolution - 1
		for i := int32(0); i < p.ResSpec; i++ {
			p.PrcwInit[i] = 256
			p.PrchInit[i] = 256
		}
	}

	p.ProgOrder = cparams.CPRL

	if p.Rsiz == cparams.ProfileCinema4K {
		p.Numpocs = initialise4KPoc(p.POC[:], p.NumResolution)
	} else {
		p.Numpocs = 0
	}

	p.CpDistoAlloc = 1
	if p.MaxCsSize <= 0 {
		p.MaxCsSize = cinema24CS
		mgr.Warnf("JPEG 2000 Profile-3 and 4 (2k/4k dc profile) requires:\n" +
			"Maximum 1302083 compressed bytes @ 24fps\n" +
			"As no rate has been given, this limit will be used.\n")
	} else if p.MaxCsSize > cinema24CS {
		mgr.Warnf("JPEG 2000 Profile-3 and 4 (2k/4k dc profile) requires:\n" +
			"Maximum 1302083 compressed bytes @ 24fps\n" +
			"-> Specified rate exceeds this limit. Rate will be forced to 1302083 bytes.\n")
		p.MaxCsSize = cinema24CS
	}

	if p.MaxCompSize <= 0 {
		p.MaxCompSize = cinema24Comp
		mgr.Warnf("JPEG 2000 Profile-3 and 4 (2k/4k dc profile) requires:\n" +
			"Maximum 1041666 compressed bytes @ 24fps\n" +
			"As no rate has been given, this limit will be used.\n")
	} else if p.MaxCompSize > cinema24Comp {
		mgr.Warnf("JPEG 2000 Profile-3 and 4 (2k/4k dc profile) requires:\n" +
			"Maximum 1041666 compressed bytes @ 24fps\n" +
			"-> Specified rate exceeds this limit. Rate will be forced to 1041666 bytes.\n")
		p.MaxCompSize = cinema24Comp
	}

	p.TcpRates[0] = float32(img.Numcomps*img.Comps[0].W*img.Comps[0].H*img.Comps[0].Prec) /
		float32(uint32(p.MaxCsSize)*8*img.Comps[0].Dx*img.Comps[0].Dy)
}

// isCinemaCompliant ports opj_j2k_is_cinema_compliant.
func isCinemaCompliant(img *image.Image, rsiz uint16, mgr *event.Manager) bool {
	if img.Numcomps != 3 {
		mgr.Warnf("JPEG 2000 Profile-3 (2k dc profile) requires:\n"+
			"3 components"+
			"-> Number of components of input image (%d) is not compliant\n"+
			"-> Non-profile-3 codestream will be generated\n", img.Numcomps)
		return false
	}
	for i := uint32(0); i < img.Numcomps; i++ {
		if img.Comps[i].Prec != 12 || img.Comps[i].Sgnd != 0 {
			tmp := "unsigned"
			if img.Comps[i].Sgnd != 0 {
				tmp = "signed"
			}
			mgr.Warnf("JPEG 2000 Profile-3 (2k dc profile) requires:\n"+
				"Precision of each component shall be 12 bits unsigned"+
				"-> At least component %d of input image (%d bits, %s) is not compliant\n"+
				"-> Non-profile-3 codestream will be generated\n", i, img.Comps[i].Prec, tmp)
			return false
		}
	}
	switch rsiz {
	case cparams.ProfileCinema2K:
		if img.Comps[0].W > 2048 || img.Comps[0].H > 1080 {
			mgr.Warnf("JPEG 2000 Profile-3 (2k dc profile) requires:\n"+
				"width <= 2048 and height <= 1080\n"+
				"-> Input image size %d x %d is not compliant\n"+
				"-> Non-profile-3 codestream will be generated\n", img.Comps[0].W, img.Comps[0].H)
			return false
		}
	case cparams.ProfileCinema4K:
		if img.Comps[0].W > 4096 || img.Comps[0].H > 2160 {
			mgr.Warnf("JPEG 2000 Profile-4 (4k dc profile) requires:\n"+
				"width <= 4096 and height <= 2160\n"+
				"-> Image size %d x %d is not compliant\n"+
				"-> Non-profile-4 codestream will be generated\n", img.Comps[0].W, img.Comps[0].H)
			return false
		}
	}
	return true
}

// getIMFMaxNL ports opj_j2k_get_imf_max_NL.
func getIMFMaxNL(p *CParameters, img *image.Image) int32 {
	rsiz := p.Rsiz
	profile := getIMFProfile(rsiz)
	xtsiz := img.X1
	if p.TileSizeOn {
		xtsiz = uint32(p.CpTdx)
	}
	switch profile {
	case profileIMF2K:
		return 5
	case profileIMF4K:
		return 6
	case profileIMF8K:
		return 7
	case profileIMF2KR:
		if xtsiz >= 2048 {
			return 5
		} else if xtsiz >= 1024 {
			return 4
		}
	case profileIMF4KR:
		if xtsiz >= 4096 {
			return 6
		} else if xtsiz >= 2048 {
			return 5
		} else if xtsiz >= 1024 {
			return 4
		}
	case profileIMF8KR:
		if xtsiz >= 8192 {
			return 7
		} else if xtsiz >= 4096 {
			return 6
		} else if xtsiz >= 2048 {
			return 5
		} else if xtsiz >= 1024 {
			return 4
		}
	}
	return -1
}

// setIMFParameters ports opj_j2k_set_imf_parameters.
func setIMFParameters(p *CParameters, img *image.Image, mgr *event.Manager) {
	rsiz := p.Rsiz
	profile := getIMFProfile(rsiz)

	if p.CblockWInit == compParamDefaultCblockW && p.CblockHInit == compParamDefaultCblockH {
		p.CblockWInit = 32
		p.CblockHInit = 32
	}

	p.TpFlag = 'C'
	p.TpOn = 1

	if p.ProgOrder == cparams.LRCP { // OPJ_COMP_PARAM_DEFAULT_PROG_ORDER
		p.ProgOrder = cparams.CPRL
	}

	if profile == profileIMF2K || profile == profileIMF4K || profile == profileIMF8K {
		p.Irreversible = 1
	}

	if p.NumResolution == compParamDefaultNumResolution && img.X0 == 0 && img.Y0 == 0 {
		maxNL := getIMFMaxNL(p, img)
		if maxNL >= 0 && p.NumResolution > maxNL {
			p.NumResolution = maxNL + 1
		}
		if !p.TileSizeOn {
			for p.NumResolution > 0 {
				if img.X1 < (uint32(1) << uint(p.NumResolution-1)) {
					p.NumResolution--
					continue
				}
				if img.Y1 < (uint32(1) << uint(p.NumResolution-1)) {
					p.NumResolution--
					continue
				}
				break
			}
		}
	}

	if p.Csty == 0 {
		p.Csty |= cparams.CPCstyPRT
		if p.NumResolution == 1 {
			p.ResSpec = 1
			p.PrcwInit[0] = 128
			p.PrchInit[0] = 128
		} else {
			p.ResSpec = p.NumResolution - 1
			for i := int32(0); i < p.ResSpec; i++ {
				p.PrcwInit[i] = 256
				p.PrchInit[i] = 256
			}
		}
	}
}

// tabMaxSubLevelFromMainLevel ports the table A.53 lookup in j2k.c.
var tabMaxSubLevelFromMainLevel = [imfMainlevelMax + 1]uint16{
	15, 1, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9,
}

// isIMFCompliant ports opj_j2k_is_imf_compliant.
func isIMFCompliant(p *CParameters, img *image.Image, mgr *event.Manager) bool {
	rsiz := p.Rsiz
	profile := getIMFProfile(rsiz)
	mainlevel := getIMFMainlevel(rsiz)
	sublevel := getIMFSublevel(rsiz)
	xtsiz := img.X1
	if p.TileSizeOn {
		xtsiz = uint32(p.CpTdx)
	}
	ret := true

	if mainlevel > imfMainlevelMax {
		mgr.Warnf("IMF profile require mainlevel <= 11.\n"+
			"-> %d is thus not compliant\n"+
			"-> Non-IMF codestream will be generated\n", mainlevel)
		ret = false
	} else if sublevel > tabMaxSubLevelFromMainLevel[mainlevel] {
		mgr.Warnf("IMF profile require sublevel <= %d for mainlevel = %d.\n"+
			"-> %d is thus not compliant\n"+
			"-> Non-IMF codestream will be generated\n",
			tabMaxSubLevelFromMainLevel[mainlevel], mainlevel, sublevel)
		ret = false
	}

	if img.Numcomps > 3 {
		mgr.Warnf("IMF profiles require at most 3 components.\n"+
			"-> Number of components of input image (%d) is not compliant\n"+
			"-> Non-IMF codestream will be generated\n", img.Numcomps)
		ret = false
	}

	if img.X0 != 0 || img.Y0 != 0 {
		mgr.Warnf("IMF profiles require image origin to be at 0,0.\n"+
			"-> %d,%d is not compliant\n"+
			"-> Non-IMF codestream will be generated\n", img.X0, boolToUint(img.Y0 != 0))
		ret = false
	}

	if p.CpTx0 != 0 || p.CpTy0 != 0 {
		mgr.Warnf("IMF profiles require tile origin to be at 0,0.\n"+
			"-> %d,%d is not compliant\n"+
			"-> Non-IMF codestream will be generated\n", p.CpTx0, p.CpTy0)
		ret = false
	}

	if p.TileSizeOn {
		if profile == profileIMF2K || profile == profileIMF4K || profile == profileIMF8K {
			if uint32(p.CpTdx) < img.X1 || uint32(p.CpTdy) < img.Y1 {
				mgr.Warnf("IMF 2K/4K/8K single tile profiles require tile to be greater or equal to image size.\n"+
					"-> %d,%d is lesser than %d,%d\n"+
					"-> Non-IMF codestream will be generated\n", p.CpTdx, p.CpTdy, img.X1, img.Y1)
				ret = false
			}
		} else {
			switch {
			case uint32(p.CpTdx) >= img.X1 && uint32(p.CpTdy) >= img.Y1:
			case p.CpTdx == 1024 && p.CpTdy == 1024:
			case p.CpTdx == 2048 && p.CpTdy == 2048 && (profile == profileIMF4K || profile == profileIMF8K):
			case p.CpTdx == 4096 && p.CpTdy == 4096 && profile == profileIMF8K:
			default:
				mgr.Warnf("IMF 2K_R/4K_R/8K_R single/multiple tile profiles "+
					"require tile to be greater or equal to image size,\n"+
					"or to be (1024,1024), or (2048,2048) for 4K_R/8K_R "+
					"or (4096,4096) for 8K_R.\n"+
					"-> %d,%d is non conformant\n"+
					"-> Non-IMF codestream will be generated\n", p.CpTdx, p.CpTdy)
				ret = false
			}
		}
	}

	for i := uint32(0); i < img.Numcomps; i++ {
		if !(img.Comps[i].Prec >= 8 && img.Comps[i].Prec <= 16) || img.Comps[i].Sgnd != 0 {
			tmp := "unsigned"
			if img.Comps[i].Sgnd != 0 {
				tmp = "signed"
			}
			mgr.Warnf("IMF profiles require precision of each component to b in [8-16] bits unsigned"+
				"-> At least component %d of input image (%d bits, %s) is not compliant\n"+
				"-> Non-IMF codestream will be generated\n", i, img.Comps[i].Prec, tmp)
			ret = false
		}
	}

	for i := uint32(0); i < img.Numcomps; i++ {
		if i == 0 && img.Comps[i].Dx != 1 {
			mgr.Warnf("IMF profiles require XRSiz1 == 1. Here it is set to %d.\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[i].Dx)
			ret = false
		}
		if i == 1 && img.Comps[i].Dx != 1 && img.Comps[i].Dx != 2 {
			mgr.Warnf("IMF profiles require XRSiz2 == 1 or 2. Here it is set to %d.\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[i].Dx)
			ret = false
		}
		if i > 1 && img.Comps[i].Dx != img.Comps[i-1].Dx {
			mgr.Warnf("IMF profiles require XRSiz%d to be the same as XRSiz2. "+
				"Here it is set to %d instead of %d.\n"+
				"-> Non-IMF codestream will be generated\n", i+1, img.Comps[i].Dx, img.Comps[i-1].Dx)
			ret = false
		}
		if img.Comps[i].Dy != 1 {
			mgr.Warnf("IMF profiles require YRsiz == 1. "+
				"Here it is set to %d for component %d.\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[i].Dy, i)
			ret = false
		}
	}

	switch profile {
	case profileIMF2K, profileIMF2KR:
		if img.Comps[0].W > 2048 || img.Comps[0].H > 1556 {
			mgr.Warnf("IMF 2K/2K_R profile require:\n"+
				"width <= 2048 and height <= 1556\n"+
				"-> Input image size %d x %d is not compliant\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[0].W, img.Comps[0].H)
			ret = false
		}
	case profileIMF4K, profileIMF4KR:
		if img.Comps[0].W > 4096 || img.Comps[0].H > 3112 {
			mgr.Warnf("IMF 4K/4K_R profile require:\n"+
				"width <= 4096 and height <= 3112\n"+
				"-> Input image size %d x %d is not compliant\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[0].W, img.Comps[0].H)
			ret = false
		}
	case profileIMF8K, profileIMF8KR:
		if img.Comps[0].W > 8192 || img.Comps[0].H > 6224 {
			mgr.Warnf("IMF 8K/8K_R profile require:\n"+
				"width <= 8192 and height <= 6224\n"+
				"-> Input image size %d x %d is not compliant\n"+
				"-> Non-IMF codestream will be generated\n", img.Comps[0].W, img.Comps[0].H)
			ret = false
		}
	default:
		return false
	}

	if p.RoiCompno != -1 {
		mgr.Warnf("IMF profile forbid RGN / region of interest marker.\n" +
			"-> Compression parameters specify a ROI\n" +
			"-> Non-IMF codestream will be generated\n")
		ret = false
	}

	if p.CblockWInit != 32 || p.CblockHInit != 32 {
		mgr.Warnf("IMF profile require code block size to be 32x32.\n"+
			"-> Compression parameters set it to %dx%d.\n"+
			"-> Non-IMF codestream will be generated\n", p.CblockWInit, p.CblockHInit)
		ret = false
	}

	if p.ProgOrder != cparams.CPRL {
		mgr.Warnf("IMF profile require progression order to be CPRL.\n"+
			"-> Compression parameters set it to %d.\n"+
			"-> Non-IMF codestream will be generated\n", p.ProgOrder)
		ret = false
	}

	if p.Numpocs != 0 {
		mgr.Warnf("IMF profile forbid POC markers.\n"+
			"-> Compression parameters set %d POC.\n"+
			"-> Non-IMF codestream will be generated\n", p.Numpocs)
		ret = false
	}

	if p.Mode != 0 {
		mgr.Warnf("IMF profile forbid mode switch in code block style.\n"+
			"-> Compression parameters set code block style to %d.\n"+
			"-> Non-IMF codestream will be generated\n", p.Mode)
		ret = false
	}

	if profile == profileIMF2K || profile == profileIMF4K || profile == profileIMF8K {
		if p.Irreversible != 1 {
			mgr.Warnf("IMF 2K/4K/8K profiles require 9-7 Irreversible Transform.\n" +
				"-> Compression parameters set it to reversible.\n" +
				"-> Non-IMF codestream will be generated\n")
			ret = false
		}
	} else {
		if p.Irreversible != 0 {
			mgr.Warnf("IMF 2K/4K/8K profiles require 5-3 reversible Transform.\n" +
				"-> Compression parameters set it to irreversible.\n" +
				"-> Non-IMF codestream will be generated\n")
			ret = false
		}
	}

	if p.TcpNumlayers != 1 {
		mgr.Warnf("IMF 2K/4K/8K profiles require 1 single quality layer.\n"+
			"-> Number of layers is %d.\n"+
			"-> Non-IMF codestream will be generated\n", p.TcpNumlayers)
		ret = false
	}

	nl := p.NumResolution - 1
	switch profile {
	case profileIMF2K:
		if !(nl >= 1 && nl <= 5) {
			mgr.Warnf("IMF 2K profile requires 1 <= NL <= 5:\n-> Number of decomposition levels is %d.\n-> Non-IMF codestream will be generated\n", nl)
			ret = false
		}
	case profileIMF4K:
		if !(nl >= 1 && nl <= 6) {
			mgr.Warnf("IMF 4K profile requires 1 <= NL <= 6:\n-> Number of decomposition levels is %d.\n-> Non-IMF codestream will be generated\n", nl)
			ret = false
		}
	case profileIMF8K:
		if !(nl >= 1 && nl <= 7) {
			mgr.Warnf("IMF 8K profile requires 1 <= NL <= 7:\n-> Number of decomposition levels is %d.\n-> Non-IMF codestream will be generated\n", nl)
			ret = false
		}
	case profileIMF2KR:
		if xtsiz >= 2048 {
			if !(nl >= 1 && nl <= 5) {
				ret = false
			}
		} else if xtsiz >= 1024 {
			if !(nl >= 1 && nl <= 4) {
				ret = false
			}
		}
	case profileIMF4KR:
		if xtsiz >= 4096 {
			if !(nl >= 1 && nl <= 6) {
				ret = false
			}
		} else if xtsiz >= 2048 {
			if !(nl >= 1 && nl <= 5) {
				ret = false
			}
		} else if xtsiz >= 1024 {
			if !(nl >= 1 && nl <= 4) {
				ret = false
			}
		}
	case profileIMF8KR:
		if xtsiz >= 8192 {
			if !(nl >= 1 && nl <= 7) {
				ret = false
			}
		} else if xtsiz >= 4096 {
			if !(nl >= 1 && nl <= 6) {
				ret = false
			}
		} else if xtsiz >= 2048 {
			if !(nl >= 1 && nl <= 5) {
				ret = false
			}
		} else if xtsiz >= 1024 {
			if !(nl >= 1 && nl <= 4) {
				ret = false
			}
		}
	}

	if p.NumResolution == 1 {
		if p.ResSpec != 1 || p.PrcwInit[0] != 128 || p.PrchInit[0] != 128 {
			ret = false
		}
	} else {
		for i := int32(0); i < p.ResSpec; i++ {
			if p.PrcwInit[i] != 256 || p.PrchInit[i] != 256 {
				ret = false
			}
		}
	}

	return ret
}

func boolToUint(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
