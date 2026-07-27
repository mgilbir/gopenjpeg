#!/bin/sh
# Regenerate the checked-in module-level oracle vectors under testdata/vectors.
#
# Usage (from anywhere):
#     tools/oracle-harness/regen.sh [OUTDIR]
#
# OUTDIR defaults to <repo>/testdata/vectors, i.e. the script overwrites the
# committed vectors in place. Pass a scratch directory to regenerate into a
# temporary tree and diff against the committed one instead:
#
#     tools/oracle-harness/regen.sh /tmp/vec && diff -r /tmp/vec testdata/vectors
#
# Requirements:
#   - a C compiler ($CC, default cc)
#   - an OpenJPEG 2.5.4 checkout, configured and built with the STATIC library,
#     at $OPENJPEG (default <repo>/oracle/openjpeg). It must contain:
#         src/lib/openjp2/*.{c,h}          the sources the harnesses #include
#         build/src/lib/openjp2/opj_config*.h  the generated config headers
#         build/bin/libopenjp2.a           the static library to link against
#     Build it with, e.g.:
#         cmake -DCMAKE_BUILD_TYPE=Release -DBUILD_STATIC_LIBS=ON \
#               -DBUILD_CODEC=ON -B build .
#         cmake --build build -j
#
# Not covered by this script (see README.md): the HTJ2K vectors in
# testdata/vectors/ht, which need an instrumented OpenJPEG build plus the
# openjpeg-data corpus, and testdata/vectors/jp2/golden.json, which is produced
# by a Go test.

set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/../.." && pwd)

OUT=${1:-$repo/testdata/vectors}
OPENJPEG=${OPENJPEG:-$repo/oracle/openjpeg}
CC=${CC:-cc}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ ! -f "$OPENJPEG/build/bin/libopenjp2.a" ]; then
    echo "regen: no static library at $OPENJPEG/build/bin/libopenjp2.a" >&2
    echo "regen: set OPENJPEG=/path/to/openjpeg (see the header of this script)" >&2
    exit 1
fi

INC="-I $OPENJPEG/src/lib/openjp2 -I $OPENJPEG/build/src/lib/openjp2"
LIB="$OPENJPEG/build/bin/libopenjp2.a -lm -lpthread"

# build <name> <source> — compile one harness into $TMP/<name>.
# -DNDEBUG matches the release semantics the Go port reproduces (it disables the
# C debug asserts in opj_int_fix_mul and friends).
build() {
    # shellcheck disable=SC2086
    $CC -O2 -DNDEBUG $INC "$here/$2" $LIB -o "$TMP/$1"
}

mkdir -p "$OUT"/opjmath "$OUT"/cio "$OUT"/bio "$OUT"/tgt "$OUT"/mqc \
         "$OUT"/dwt "$OUT"/sparse "$OUT"/image "$OUT"/mct "$OUT"/t1 \
         "$OUT"/pi "$OUT"/t2

echo "regen: W1 (opjmath, cio, bio, tgt)"
build opjmath_vectors w1/opjmath_vectors.c
build cio_vectors     w1/cio_vectors.c
build bio_vectors     w1/bio_vectors.c
build tgt_vectors     w1/tgt_vectors.c
"$TMP/opjmath_vectors" > "$OUT/opjmath/intmath.txt"
"$TMP/cio_vectors"     > "$OUT/cio/bytes.txt"
"$TMP/bio_vectors"     > "$OUT/bio/bio.txt"
"$TMP/tgt_vectors"     > "$OUT/tgt/tgt.txt"

echo "regen: W2 (mqc)"
build mqc_gen w2/gen.c
"$TMP/mqc_gen" > "$OUT/mqc/mqc_vectors.json"

echo "regen: W3 (dwt, sparse)"
build dwt_gen     w3/dwt_gen.c
build partial_gen w3/partial_gen.c
build norms_gen   w3/norms_gen.c
build sparse_gen  w3/sparse_gen.c
"$TMP/dwt_gen"     "$OUT/dwt/whole.bin"
"$TMP/partial_gen" "$OUT/dwt/partial.bin"
"$TMP/norms_gen"   "$OUT/dwt/norms.bin"
"$TMP/sparse_gen"  "$OUT/sparse/vectors.bin"

echo "regen: W4 (image, mct)"
build image_gen w4/image_gen.c
build mct_gen   w4/mct_gen.c
"$TMP/image_gen" > "$OUT/image/vectors.json"
# NOTE: mct/vectors.json is NOT fully reproducible from the static library. The
# ict.dec_c1 array is taken from a build linked against the SHIPPED shared
# library, which -ffast-math reassociates differently; see the provenance
# section of testdata/vectors/mct/README.md before committing a regenerated
# file.
"$TMP/mct_gen" > "$OUT/mct/vectors.json"

echo "regen: W5 (t1)"
# harness.c writes to the fixed relative path testdata/vectors/t1/, so give it a
# staging tree with that shape and move the results into place afterwards.
build t1_harness w5/harness.c
mkdir -p "$TMP/stage/testdata/vectors/t1"
(cd "$TMP/stage" && "$TMP/t1_harness")
mv "$TMP/stage/testdata/vectors/t1/t1_encode.bin" \
   "$TMP/stage/testdata/vectors/t1/t1_decode.bin" "$OUT/t1/"
# -n drops the name and mtime gzip would otherwise embed, so two runs of this
# script produce identical .gz bytes. The committed files predate that flag, so
# compare them by decompressed payload (see README.md).
gzip -9 -n -f "$OUT/t1/t1_encode.bin" "$OUT/t1/t1_decode.bin"

echo "regen: W6 (pi, t2)"
build pi_harness w6/pi_harness.c
build t2_harness w6/t2_harness.c
"$TMP/pi_harness" "$OUT/pi/pi_vectors.txt"
"$TMP/t2_harness" "$OUT/t2/t2_vectors.txt"

echo "regen: done -> $OUT"
