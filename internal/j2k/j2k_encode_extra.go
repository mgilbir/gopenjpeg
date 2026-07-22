package j2k

// Encoder extra-option handling and PLT (packet length, tile-part) marker
// emission. Ports opj_j2k_encoder_set_extra_options and
// opj_j2k_write_plt_in_memory.

import (
	"strconv"
	"strings"

	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// EncoderSetExtraOptions ports opj_j2k_encoder_set_extra_options: parse the
// PLT=, TLM= and GUARD_BITS= options. Unknown options return ErrEncodeSetup.
func (e *Encoder) EncoderSetExtraOptions(options []string, mgr *event.Manager) error {
	for _, opt := range options {
		switch {
		case strings.HasPrefix(opt, "PLT="):
			switch opt {
			case "PLT=YES":
				e.enc.plt = true
			case "PLT=NO":
				e.enc.plt = false
			default:
				mgr.Errorf("Invalid value for option: %s.\n", opt)
				return ErrEncodeSetup
			}
		case strings.HasPrefix(opt, "TLM="):
			switch opt {
			case "TLM=YES":
				e.enc.tlm = true
			case "TLM=NO":
				e.enc.tlm = false
			default:
				mgr.Errorf("Invalid value for option: %s.\n", opt)
				return ErrEncodeSetup
			}
		case strings.HasPrefix(opt, "GUARD_BITS="):
			numgbits, err := strconv.Atoi(opt[len("GUARD_BITS="):])
			if err != nil || numgbits < 0 || numgbits > 7 {
				mgr.Errorf("Invalid value for option: %s. Should be in [0,7]\n", opt)
				return ErrEncodeSetup
			}
			nbTiles := e.CP.Tw * e.CP.Th
			for tileno := uint32(0); tileno < nbTiles; tileno++ {
				tcp := &e.CP.Tcps[tileno]
				for i := uint32(0); i < e.enc.nbComps; i++ {
					tcp.TCCPs[i].Numgbits = uint32(numgbits)
				}
			}
		default:
			mgr.Errorf("Invalid option: %s.\n", opt)
			return ErrEncodeSetup
		}
	}
	return nil
}

// writePLTInMemory ports opj_j2k_write_plt_in_memory: serialise the per-packet
// sizes captured in markerInfo into one or more PLT marker segments in data,
// returning the number of bytes written.
func writePLTInMemory(markerInfo *tile.MarkerInfo, data []byte, mgr *event.Manager) (uint32, error) {
	var zplt uint32
	start := 0
	pos := 0

	cio.WriteBytes(data[pos:], msPLT, 2)
	pos += 2
	lpltPos := pos // reserve 2 bytes for Lplt
	pos += 2
	cio.WriteBytes(data[pos:], zplt, 1)
	pos++

	lplt := uint32(3)

	for i := uint32(0); i < markerInfo.PacketCount; i++ {
		var varBytes [5]byte
		varBytesSize := 0
		packetSize := markerInfo.PacketSize[i]

		varBytes[varBytesSize] = byte(packetSize & 0x7f)
		varBytesSize++
		packetSize >>= 7
		for packetSize > 0 {
			varBytes[varBytesSize] = byte((packetSize & 0x7f) | 0x80)
			varBytesSize++
			packetSize >>= 7
		}

		if lplt+uint32(varBytesSize) > 65535 {
			if zplt == 255 {
				mgr.Errorf("More than 255 PLT markers would be needed for current tile-part !\n")
				return 0, ErrEncodeWrite
			}
			cio.WriteBytes(data[lpltPos:], lplt, 2)

			cio.WriteBytes(data[pos:], msPLT, 2)
			pos += 2
			lpltPos = pos
			pos += 2
			zplt++
			cio.WriteBytes(data[pos:], zplt, 1)
			pos++
			lplt = 3
		}

		lplt += uint32(varBytesSize)

		for ; varBytesSize > 0; varBytesSize-- {
			cio.WriteBytes(data[pos:], uint32(varBytes[varBytesSize-1]), 1)
			pos++
		}
	}

	cio.WriteBytes(data[lpltPos:], lplt, 2)
	return uint32(pos - start), nil
}
