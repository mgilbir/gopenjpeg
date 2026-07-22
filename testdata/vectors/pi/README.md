# W6 packet-iterator (pi) oracle vectors

`pi_vectors.txt` drives `internal/pi` `TestPiVectors`. Each config is
self-describing: it carries every parameter needed to rebuild the image / cp /
tcp / tccp on the Go side, plus the full decode and encode iteration sequences
produced by the C reference (`opj_pi_create_decode` + `opj_pi_next`, and
`opj_pi_initialise_encode` + `opj_pi_create_encode` + `opj_pi_next`).

The Go test rebuilds the identical structures, runs the Go packet iterator, and
checks the `(compno, resno, precno, layno)` sequence matches exactly.

## Coverage (168 configs)

- All 5 progression orders (LRCP/RLCP/RPCL/PCRL/CPRL).
- Image sizes with and without a nonzero origin; 1–4 components; chroma
  subsampling (dx/dy = 2).
- 1–6 resolution levels; default (15) and custom per-resolution precinct sizes.
- 1–5 quality layers.
- Single tile and multi-tile grids (an interior/edge tile is dumped).
- POC: single-record, two-record (RLCP→CPRL), and three-record staged
  progressions.

Each iteration sequence is capped at 5000 packets (a `capped` flag follows the
count on the `DECODE`/`ENCODE` line); no config in the current matrix reaches
the cap.

## Format (text, line-oriented)

```
PIVEC 1 <nconfigs>
CONFIG <name>
IMG <x0> <y0> <x1> <y1>
COMPS <numcomps>
COMP <c> <dx> <dy> <numres>
RES  <c> <r> <prcw> <prch>          # numres lines per comp
TILE <tw> <th> <tdx> <tdy> <tx0> <ty0> <tileno>
PRG  <prg> <numlayers>
POC  <use_poc> <numpocs>            # numpocs = tcp->numpocs
POCLINE <i> <prg> <resno0> <resno1> <compno0> <compno1> <layno1>   # if use_poc
DECODE <count> <capped>
<compno> <resno> <precno> <layno>   # count lines
ENCODE <count> <capped>
<compno> <resno> <precno> <layno>   # count lines
```

## Regenerate

```sh
gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w6/pi_harness.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6pi
/tmp/w6pi testdata/vectors/pi/pi_vectors.txt
```

`pi_harness.c` links the non-static pi.c entry points from `libopenjp2.a`; the
whole `oracle/` tree is gitignored.
