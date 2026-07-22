# tgt oracle vectors

`tgt.txt` holds tag-tree encode/decode vectors for `internal/tgt` (ported from
`tgt.c`), generated with the C `opj_tgt_*` functions coding through the C
`opj_bio_*` bit stream.

## Format

Four lines per case:

```
TREE <H> <V> <threshold>
VALS <v0> <v1> ...             # leaf values in leafno order
ENC  <hexbytes>                # encode every leaf at threshold, then flush
DEC  <r0>:<val0> <r1>:<val1>   # fresh-tree decode of every leaf
```

`ENC` is produced by creating a tree of `H x V` leaves, `opj_tgt_setvalue` for
each leaf, then `opj_tgt_encode(bio, leaf, threshold)` for every leaf followed
by `opj_bio_flush`.

`DEC` records, for a *fresh* tree decoding `ENC`, each leaf's
`opj_tgt_decode` return value (`r`, 1 iff decoded value < threshold) and the
resulting `tree->nodes[leaf].value`.

Cases sweep dimensions {1x1, 1x3, 3x1, 2x2, 3x3, 4x2, 5x5, 8x1, 1x8, 4x4, 6x3,
7x7} × thresholds {1, 3, 5, 10, 20, 25} with fixed-seed pseudo-random leaf
values in [0,20), covering both the `decode==1` and `decode==0` outcomes.

## Regeneration

```
gcc -O2 -DNDEBUG \
    -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w1/tgt_vectors.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/tgt_vectors
/tmp/tgt_vectors > testdata/vectors/tgt/tgt.txt
```
