package j2k

// Tile-part write flow and end-of-compression, port of opj_j2k_encode,
// opj_j2k_post_write_tile, write_first_tile_part / write_all_tile_parts,
// write_sot / write_sod, update_tlm, write_eoc / write_updated_tlm.

import (
	"github.com/mgilbir/gopenjpeg/internal/cio"
	"github.com/mgilbir/gopenjpeg/internal/cparams"
	"github.com/mgilbir/gopenjpeg/internal/event"
	"github.com/mgilbir/gopenjpeg/internal/tile"
)

// Encode ports opj_j2k_encode: encode every tile of the image.
func (e *Encoder) Encode(stream *cio.Stream, mgr *event.Manager) error {
	nbTiles := e.CP.Th * e.CP.Tw
	for i := uint32(0); i < nbTiles; i++ {
		if err := e.preWriteTile(i, mgr); err != nil {
			return err
		}
		// Allocate tile-component data and copy the image samples in.
		if !e.tcd.AllocTileComponentData() {
			mgr.Errorf("Error allocating tile component data.")
			return ErrEncodeTile
		}
		tileSize := e.tcd.GetEncoderInputBufferSize()
		buf := make([]byte, tileSize)
		e.tcd.GetTileData(buf)
		if err := e.tcd.CopyTileData(buf); err != nil {
			mgr.Errorf("Size mismatch between tile data and sent data.")
			return err
		}
		if err := e.postWriteTile(stream, mgr); err != nil {
			return err
		}
	}
	return nil
}

// preWriteTile ports opj_j2k_pre_write_tile.
func (e *Encoder) preWriteTile(tileIndex uint32, mgr *event.Manager) error {
	if tileIndex != e.currentTileNumber {
		mgr.Errorf("The given tile index does not match.")
		return ErrEncodeTile
	}
	mgr.Infof("tile number %d / %d\n", e.currentTileNumber+1, e.CP.Tw*e.CP.Th)
	e.enc.currentTilePartNumber = 0
	e.tcd.CurTotnumTp = e.CP.Tcps[tileIndex].MNbTileParts
	e.enc.currentPocTilePartNumber = 0
	if err := e.tcd.InitEncodeTile(e.currentTileNumber, mgr); err != nil {
		return err
	}
	return nil
}

// postWriteTile ports opj_j2k_post_write_tile.
func (e *Encoder) postWriteTile(stream *cio.Stream, mgr *event.Manager) error {
	tileSize := e.enc.encodedTileSize
	availableData := tileSize
	data := e.enc.encodedTileData

	nb, err := e.writeFirstTilePart(data, availableData, stream, mgr)
	if err != nil {
		return err
	}
	data = data[nb:]
	availableData -= nb

	nb2, err := e.writeAllTileParts(data, availableData, stream, mgr)
	if err != nil {
		return err
	}
	availableData -= nb2

	nbBytesWritten := tileSize - availableData
	if err := writeToStream(stream, e.enc.encodedTileData[:nbBytesWritten], mgr); err != nil {
		return err
	}
	e.currentTileNumber++
	return nil
}

// writeFirstTilePart ports opj_j2k_write_first_tile_part.
func (e *Encoder) writeFirstTilePart(data []byte, totalDataSize uint32,
	stream *cio.Stream, mgr *event.Manager) (uint32, error) {
	nbBytesWritten := uint32(0)
	e.tcd.CurPino = 0
	e.enc.currentPocTilePartNumber = 0

	beginData := data
	cur, err := e.writeSOT(data, totalDataSize, mgr)
	if err != nil {
		return 0, err
	}
	nbBytesWritten += cur
	data = data[cur:]
	totalDataSize -= cur

	if !cparams.IsCinema(e.CP.Rsiz) {
		if e.CP.Tcps[e.currentTileNumber].POC != 0 {
			written := uint32(0)
			e.writePOCInMemory(data, &written, mgr)
			nbBytesWritten += written
			data = data[written:]
			totalDataSize -= written
		}
	}

	sodWritten, err := e.writeSOD(data, totalDataSize, mgr)
	if err != nil {
		return 0, err
	}
	nbBytesWritten += sodWritten

	// Patch Psot in the SOT marker.
	cio.WriteBytes(beginData[6:], nbBytesWritten, 4)

	if e.enc.tlm {
		e.updateTLM(nbBytesWritten)
	}
	return nbBytesWritten, nil
}

// writeAllTileParts ports opj_j2k_write_all_tile_parts.
func (e *Encoder) writeAllTileParts(data []byte, totalDataSize uint32,
	stream *cio.Stream, mgr *event.Manager) (uint32, error) {
	nbBytesWritten := uint32(0)
	tcp := &e.CP.Tcps[e.currentTileNumber]

	totNumTp := e.getNumTP(0, e.currentTileNumber)

	e.enc.currentTilePartNumber++
	for tilepartno := uint32(1); tilepartno < totNumTp; tilepartno++ {
		e.enc.currentPocTilePartNumber = tilepartno
		partTileSize := uint32(0)
		beginData := data

		cur, err := e.writeSOT(data, totalDataSize, mgr)
		if err != nil {
			return 0, err
		}
		nbBytesWritten += cur
		data = data[cur:]
		totalDataSize -= cur
		partTileSize += cur

		sod, err := e.writeSOD(data, totalDataSize, mgr)
		if err != nil {
			return 0, err
		}
		data = data[sod:]
		nbBytesWritten += sod
		totalDataSize -= sod
		partTileSize += sod

		cio.WriteBytes(beginData[6:], partTileSize, 4)
		if e.enc.tlm {
			e.updateTLM(partTileSize)
		}
		e.enc.currentTilePartNumber++
	}

	for pino := uint32(1); pino <= tcp.Numpocs; pino++ {
		e.tcd.CurPino = pino
		totNumTp = e.getNumTP(pino, e.currentTileNumber)
		for tilepartno := uint32(0); tilepartno < totNumTp; tilepartno++ {
			e.enc.currentPocTilePartNumber = tilepartno
			partTileSize := uint32(0)
			beginData := data

			cur, err := e.writeSOT(data, totalDataSize, mgr)
			if err != nil {
				return 0, err
			}
			nbBytesWritten += cur
			data = data[cur:]
			totalDataSize -= cur
			partTileSize += cur

			sod, err := e.writeSOD(data, totalDataSize, mgr)
			if err != nil {
				return 0, err
			}
			nbBytesWritten += sod
			data = data[sod:]
			totalDataSize -= sod
			partTileSize += sod

			cio.WriteBytes(beginData[6:], partTileSize, 4)
			if e.enc.tlm {
				e.updateTLM(partTileSize)
			}
			e.enc.currentTilePartNumber++
		}
	}
	return nbBytesWritten, nil
}

// writeSOT ports opj_j2k_write_sot: writes 12 bytes (Psot left as 0 for now).
func (e *Encoder) writeSOT(data []byte, totalDataSize uint32, mgr *event.Manager) (uint32, error) {
	if totalDataSize < 12 {
		mgr.Errorf("Not enough bytes in output buffer to write SOT marker\n")
		return 0, ErrEncodeWrite
	}
	cio.WriteBytes(data, msSOT, 2)
	cio.WriteBytes(data[2:], 10, 2)
	cio.WriteBytes(data[4:], e.currentTileNumber, 2)
	// Psot (data[6:10]) written later.
	cio.WriteBytes(data[10:], e.enc.currentTilePartNumber, 1)
	cio.WriteBytes(data[11:], e.CP.Tcps[e.currentTileNumber].MNbTileParts, 1)
	return 12, nil
}

// writeSOD ports opj_j2k_write_sod: SOD marker + tile-part packets via tcd.
func (e *Encoder) writeSOD(data []byte, totalDataSize uint32, mgr *event.Manager) (uint32, error) {
	if totalDataSize < 4 {
		mgr.Errorf("Not enough bytes in output buffer to write SOD marker\n")
		return 0, ErrEncodeWrite
	}
	cio.WriteBytes(data, msSOD, 2)
	remaining := totalDataSize - 4

	e.tcd.TpNum = e.enc.currentPocTilePartNumber
	e.tcd.CurTpNum = e.enc.currentTilePartNumber

	var markerInfo *tile.MarkerInfo
	if e.enc.plt {
		markerInfo = &tile.MarkerInfo{NeedPLT: true}
	}
	if remaining < e.enc.reservedBytesPLT {
		mgr.Errorf("Not enough bytes in output buffer to write SOD marker\n")
		return 0, ErrEncodeWrite
	}
	remaining -= e.enc.reservedBytesPLT

	written, err := e.tcd.EncodeTile(e.currentTileNumber, data[2:], remaining, markerInfo, mgr)
	if err != nil {
		mgr.Errorf("Cannot encode tile\n")
		return 0, err
	}
	written += 2 // SOD marker

	if e.enc.plt {
		// Serialise PLT marker(s) into a temporary buffer, then move the SOD +
		// packet data forward and prepend the PLT (opj_j2k_write_sod).
		pltBuf := make([]byte, e.enc.reservedBytesPLT)
		pltWritten, perr := writePLTInMemory(markerInfo, pltBuf, mgr)
		if perr != nil {
			return 0, perr
		}
		if pltWritten > e.enc.reservedBytesPLT {
			mgr.Errorf("PLT marker overflow\n")
			return 0, ErrEncodeWrite
		}
		copy(data[pltWritten:pltWritten+written], data[:written])
		copy(data[:pltWritten], pltBuf[:pltWritten])
		written += pltWritten
	}
	return written, nil
}

// updateTLM ports opj_j2k_update_tlm.
func (e *Encoder) updateTLM(tilePartSize uint32) {
	if e.enc.ttlmiIsByte {
		cio.WriteBytes(e.enc.tlmBuffer[e.enc.tlmCurrent:], e.currentTileNumber, 1)
		e.enc.tlmCurrent++
	} else {
		cio.WriteBytes(e.enc.tlmBuffer[e.enc.tlmCurrent:], e.currentTileNumber, 2)
		e.enc.tlmCurrent += 2
	}
	cio.WriteBytes(e.enc.tlmBuffer[e.enc.tlmCurrent:], tilePartSize, 4)
	e.enc.tlmCurrent += 4
}

// EndCompress ports opj_j2k_end_compress.
func (e *Encoder) EndCompress(stream *cio.Stream, mgr *event.Manager) error {
	if err := e.writeEOC(stream, mgr); err != nil {
		return err
	}
	if e.enc.tlm {
		if err := e.writeUpdatedTLM(stream, mgr); err != nil {
			return err
		}
	}
	// write_epc is a no-op without a codestream index.
	// end_encoding / destroy_header_memory free state (GC handles that here).
	e.tcd = nil
	return nil
}

// writeEOC ports opj_j2k_write_eoc.
func (e *Encoder) writeEOC(stream *cio.Stream, mgr *event.Manager) error {
	buf := make([]byte, 2)
	cio.WriteBytes(buf, msEOC, 2)
	if err := writeToStream(stream, buf, mgr); err != nil {
		return err
	}
	if err := stream.Flush(mgr); err != nil {
		return err
	}
	return nil
}

// writeUpdatedTLM ports opj_j2k_write_updated_tlm: seek back and patch the TLM.
func (e *Encoder) writeUpdatedTLM(stream *cio.Stream, mgr *event.Manager) error {
	sizePerTilePart := uint32(6)
	if e.enc.ttlmiIsByte {
		sizePerTilePart = 5
	}
	tlmSize := sizePerTilePart * e.enc.totalTileParts
	tlmPosition := 6 + e.enc.tlmStart
	currentPosition := stream.Tell()

	if err := stream.SeekTo(tlmPosition, mgr); err != nil {
		return err
	}
	if err := writeToStream(stream, e.enc.tlmBuffer[:tlmSize], mgr); err != nil {
		return err
	}
	if err := stream.SeekTo(currentPosition, mgr); err != nil {
		return err
	}
	return nil
}
