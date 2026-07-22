# cio oracle vectors

`bytes.txt` holds byte-order (de)serialisation vectors for the helpers in
`internal/cio/bytes.go` (ported from `cio.c`).

## Background: the _BE/_LE C helpers

The C helpers come in `_BE` / `_LE` variants that are *host-endianness
specialisations*: on a given CPU one does a raw `memcpy` and the other
byte-swaps. `cio.h` selects between them with a macro (`opj_write_bytes`, …)
keyed on `OPJ_BIG_ENDIAN`, so the macro's serialisation is **always
big-endian**, and the whole OpenJPEG codebase uses only the macros.

The Go port instead exposes functions by their *true* semantics
(`WriteBytesBE` = big-endian, `WriteBytesLE` = little-endian) plus the
big-endian macro equivalents (`WriteBytes`, `ReadBytes`, `WriteDouble`, …).

The harness therefore detects host endianness and, for each true-endian label,
calls the C helper that realises it (mirroring the macro dispatch and
exercising both helpers).

## Format

Whitespace-separated lines:

- `wb_be <value_u32> <nb> <hexbytes>` — WriteBytesBE(value, nb) → bytes
- `wb_le <value_u32> <nb> <hexbytes>` — WriteBytesLE, emitted for `nb==4` only
- `rb_be <hexbytes> <nb> <value_u32>` — ReadBytesBE(bytes, nb) → value
- `rb_le <hexbytes> <nb> <value_u32>` — ReadBytesLE, `nb==4` only
- `wd_be`/`wd_le <bits_u64_hex> <hexbytes8>` — Write{Double}{BE,LE}
- `rd_be`/`rd_le <hexbytes8> <bits_u64_hex>` — Read{Double}{BE,LE}
- `wf_be`/`wf_le <bits_u32_hex> <hexbytes4>` — Write{Float}{BE,LE}
- `rf_be`/`rf_le <hexbytes4> <bits_u32_hex>` — Read{Float}{BE,LE}

Floats/doubles are exchanged as raw bit patterns so the vectors are exact
(includes ±0, ±inf, NaN). Little-endian integer vectors are emitted at full
width only, because the C `_BE` helper (which realises true little-endian on a
little-endian host) does not coincide with a clean little-endian-of-low-nbBytes
serialiser for partial widths; partial-width LE is covered by Go-native tests.

## Regeneration

```
gcc -O2 -DNDEBUG \
    -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    oracle/harness/w1/cio_vectors.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/cio_vectors
/tmp/cio_vectors > testdata/vectors/cio/bytes.txt
```
