# W6 tier-2 (t2) oracle vectors

`t2_vectors.txt` drives `internal/t2` `TestT2Vectors`. Each case is
self-describing: it carries the full synthetic tile geometry (bands, precincts,
code-blocks, per-pass lengths/term flags, per-layer data), the packet bytes
produced by `opj_t2_encode_packets`, and the per-code-block results of
`opj_t2_decode_packets` run on a mirror (empty) tile over those bytes.

The Go test rebuilds the identical encode tile, runs the Go encoder, and checks
the output is **byte-identical**. It then rebuilds the empty decode tile, runs
the Go decoder over the (possibly truncated) bytes, and checks the per-cblk
segment lengths / pass counts / concatenated chunk data and the success flag
match — including the warning-vs-error tolerance classification.

## Geometry convention

Every band and precinct is a uniform `(0,0,8,8)` rectangle with one precinct per
resolution (default precinct exponent 15 ⇒ `pw=ph=1`, so `precno` is always 0).
The decode window covers the whole reference grid, so the whole-tile
area-of-interest check (`WholeTileAOI` in Go, `opj_tcd_is_subband_area_of_interest`
with a full window in C) keeps every packet. This isolates the t2 packet-coding
logic, which is what these vectors validate. Per-pass lengths and per-layer data
bytes come from a deterministic PRNG in the harness and are dumped verbatim, so
the Go side reproduces the exact same tile without re-implementing the PRNG.

## Coverage (52 cases)

- All 5 progressions × csty=0, and all 4 csty combinations
  ({0, SOP, EPH, SOP|EPH}) × LRCP, across 5 geometry variants
  (1–2 comps, 1–3 res, 1×1/2×1/1×2/2×2 code-block grids, 1–3 layers).
- Code-block styles TERMALL (term on each pass) and LAZY (segment splitting).
- Partial-layer decode (`num_layers_to_decode` < layers) and
  `maxlayers` < layers on encode.
- Truncated decode, non-strict (tolerant: warning, code-blocks marked
  `corrupted`, decode returns success) and strict (hard error, decode fails).

## Format (text, line-oriented)

```
T2VEC 1 <ncases>
CASE <name> csty N prg N maxlayers N numcomps N numres N cw N ch N
     numlayers N ppl N cblksty N term_each N imgw N imgh N ltd N trunc N strict N
COMP  <c> numres N
BAND  <c> <r> <bandno> numbps N cw N ch N
CBLK  <c> <r> <bandno> <k> numbps N numlayers N totalpasses N
LAYER <c> <r> <bandno> <k> <layno> np N len N data <hex|- >
PASS  <c> <r> <bandno> <k> <passno> len N term N
ENC   <ok> <written> <hex|- >
DEC   <ok> <read> declen N
DCBLK <c> <r> <bandno> <k> numsegs N realnumsegs N corrupted N numchunks N
DSEG  <c> <r> <bandno> <k> <segno> len N numpasses N realnumpasses N
DDATA <c> <r> <bandno> <k> <total> <hex|- >   # concatenated chunk bytes
```

`ltd` = num_layers_to_decode; `ppl` = passes per layer; `trunc` = decode byte
cap (0 = none). Hex `-` means an empty byte string.

## Regenerate

```sh
gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w6/t2_harness.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6t2
/tmp/w6t2 testdata/vectors/t2/t2_vectors.txt
```

`t2_harness.c` links the non-static t2.c entry points from `libopenjp2.a`; the
whole `oracle/` tree is gitignored.
