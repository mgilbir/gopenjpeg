package tcd

import "errors"

var (
	errTileGeometry    = errors.New("tcd: invalid tile geometry")
	errIntegerOverflow = errors.New("tcd: integer overflow")
	errAlloc           = errors.New("tcd: allocation failure")
	errTierDecode      = errors.New("tcd: tier decode failed")
	errMCT             = errors.New("tcd: MCT step failed")
)
