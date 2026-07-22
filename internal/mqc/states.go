// Package mqc is a pure-Go, bit-exact port of the OpenJPEG MQ arithmetic
// coder (mqc.c, mqc.h, mqc_inl.h). It implements the JPEG 2000 entropy coder
// used by tier-1 (EBCOT) coding: the adaptive MQ arithmetic coder plus the
// RAW/BYPASS raw bit coder.
//
// The port mirrors the C control flow closely; every exported method carries
// the name of the C function it corresponds to in its doc comment. Integer
// widths match the C source (OPJ_UINT32 -> uint32, etc.).
package mqc

// mqcState is the Go equivalent of opj_mqc_state_t. In the C reference each
// state stores raw pointers to the next MPS/LPS states; here we store indices
// into the states table instead (never unsafe pointers).
type mqcState struct {
	// qeval is the probability of the Least Probable Symbol.
	qeval uint32
	// mps is the Most Probable Symbol (0 or 1).
	mps uint32
	// nmps is the index of the next state if the next symbol is the MPS.
	nmps int32
	// nlps is the index of the next state if the next symbol is the LPS.
	nlps int32
}

// states is the direct transcription of the C mqc_states[47*2] table. The
// pointer references &mqc_states[N] in the C source are represented here as
// the integer index N.
var states = [94]mqcState{
	/*  0 */ {0x5601, 0, 2, 3},
	/*  1 */ {0x5601, 1, 3, 2},
	/*  2 */ {0x3401, 0, 4, 12},
	/*  3 */ {0x3401, 1, 5, 13},
	/*  4 */ {0x1801, 0, 6, 18},
	/*  5 */ {0x1801, 1, 7, 19},
	/*  6 */ {0x0ac1, 0, 8, 24},
	/*  7 */ {0x0ac1, 1, 9, 25},
	/*  8 */ {0x0521, 0, 10, 58},
	/*  9 */ {0x0521, 1, 11, 59},
	/* 10 */ {0x0221, 0, 76, 66},
	/* 11 */ {0x0221, 1, 77, 67},
	/* 12 */ {0x5601, 0, 14, 13},
	/* 13 */ {0x5601, 1, 15, 12},
	/* 14 */ {0x5401, 0, 16, 28},
	/* 15 */ {0x5401, 1, 17, 29},
	/* 16 */ {0x4801, 0, 18, 28},
	/* 17 */ {0x4801, 1, 19, 29},
	/* 18 */ {0x3801, 0, 20, 28},
	/* 19 */ {0x3801, 1, 21, 29},
	/* 20 */ {0x3001, 0, 22, 34},
	/* 21 */ {0x3001, 1, 23, 35},
	/* 22 */ {0x2401, 0, 24, 36},
	/* 23 */ {0x2401, 1, 25, 37},
	/* 24 */ {0x1c01, 0, 26, 40},
	/* 25 */ {0x1c01, 1, 27, 41},
	/* 26 */ {0x1601, 0, 58, 42},
	/* 27 */ {0x1601, 1, 59, 43},
	/* 28 */ {0x5601, 0, 30, 29},
	/* 29 */ {0x5601, 1, 31, 28},
	/* 30 */ {0x5401, 0, 32, 28},
	/* 31 */ {0x5401, 1, 33, 29},
	/* 32 */ {0x5101, 0, 34, 30},
	/* 33 */ {0x5101, 1, 35, 31},
	/* 34 */ {0x4801, 0, 36, 32},
	/* 35 */ {0x4801, 1, 37, 33},
	/* 36 */ {0x3801, 0, 38, 34},
	/* 37 */ {0x3801, 1, 39, 35},
	/* 38 */ {0x3401, 0, 40, 36},
	/* 39 */ {0x3401, 1, 41, 37},
	/* 40 */ {0x3001, 0, 42, 38},
	/* 41 */ {0x3001, 1, 43, 39},
	/* 42 */ {0x2801, 0, 44, 38},
	/* 43 */ {0x2801, 1, 45, 39},
	/* 44 */ {0x2401, 0, 46, 40},
	/* 45 */ {0x2401, 1, 47, 41},
	/* 46 */ {0x2201, 0, 48, 42},
	/* 47 */ {0x2201, 1, 49, 43},
	/* 48 */ {0x1c01, 0, 50, 44},
	/* 49 */ {0x1c01, 1, 51, 45},
	/* 50 */ {0x1801, 0, 52, 46},
	/* 51 */ {0x1801, 1, 53, 47},
	/* 52 */ {0x1601, 0, 54, 48},
	/* 53 */ {0x1601, 1, 55, 49},
	/* 54 */ {0x1401, 0, 56, 50},
	/* 55 */ {0x1401, 1, 57, 51},
	/* 56 */ {0x1201, 0, 58, 52},
	/* 57 */ {0x1201, 1, 59, 53},
	/* 58 */ {0x1101, 0, 60, 54},
	/* 59 */ {0x1101, 1, 61, 55},
	/* 60 */ {0x0ac1, 0, 62, 56},
	/* 61 */ {0x0ac1, 1, 63, 57},
	/* 62 */ {0x09c1, 0, 64, 58},
	/* 63 */ {0x09c1, 1, 65, 59},
	/* 64 */ {0x08a1, 0, 66, 60},
	/* 65 */ {0x08a1, 1, 67, 61},
	/* 66 */ {0x0521, 0, 68, 62},
	/* 67 */ {0x0521, 1, 69, 63},
	/* 68 */ {0x0441, 0, 70, 64},
	/* 69 */ {0x0441, 1, 71, 65},
	/* 70 */ {0x02a1, 0, 72, 66},
	/* 71 */ {0x02a1, 1, 73, 67},
	/* 72 */ {0x0221, 0, 74, 68},
	/* 73 */ {0x0221, 1, 75, 69},
	/* 74 */ {0x0141, 0, 76, 70},
	/* 75 */ {0x0141, 1, 77, 71},
	/* 76 */ {0x0111, 0, 78, 72},
	/* 77 */ {0x0111, 1, 79, 73},
	/* 78 */ {0x0085, 0, 80, 74},
	/* 79 */ {0x0085, 1, 81, 75},
	/* 80 */ {0x0049, 0, 82, 76},
	/* 81 */ {0x0049, 1, 83, 77},
	/* 82 */ {0x0025, 0, 84, 78},
	/* 83 */ {0x0025, 1, 85, 79},
	/* 84 */ {0x0015, 0, 86, 80},
	/* 85 */ {0x0015, 1, 87, 81},
	/* 86 */ {0x0009, 0, 88, 82},
	/* 87 */ {0x0009, 1, 89, 83},
	/* 88 */ {0x0005, 0, 90, 84},
	/* 89 */ {0x0005, 1, 91, 85},
	/* 90 */ {0x0001, 0, 90, 86},
	/* 91 */ {0x0001, 1, 91, 87},
	/* 92 */ {0x5601, 0, 92, 92},
	/* 93 */ {0x5601, 1, 93, 93},
}
