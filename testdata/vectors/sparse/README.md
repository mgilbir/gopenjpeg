# sparse oracle vectors

`vectors.bin` is a deterministic op-stream generated from the OpenJPEG C
library (`opj_sparse_array_int32_*`) and replayed bit-for-bit against the Go
port in `internal/sparse` (`TestSparseVectors`).

## Regeneration

```sh
gcc -O2 oracle/harness/w3/sparse_gen.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/sparse_gen
/tmp/sparse_gen testdata/vectors/sparse/vectors.bin
```

The harness (`oracle/harness/w3/sparse_gen.c`, gitignored) exercises a matrix of
array/block layouts with writes, reads, strided reads (col strides 1/2/3/4/8),
sub-region reads (including 1-pixel, single-row, single-column), invalid-region
reads/writes in both forgiving and strict modes, and arrays left with
unwritten (zero) blocks.

## Format (little-endian)

```
u32  ncfg
repeat ncfg:
  u32 w, u32 h, u32 block_width, u32 block_height   # informational header
  u32 nops
  repeat nops:
    u32 op_type          # 0=write, 1=read, 2=create
    if op_type == 2 (create):
      u32 w, u32 h, u32 block_width, u32 block_height
    else (read/write):
      u32 x0, y0, x1, y1
      u32 col_stride, line_stride   # in int32 elements
      u32 forgiving                 # 0/1
      u32 retval                    # OPJ_BOOL returned by the C call
      u32 buflen                    # number of int32 elements in buffer
      i32 buffer[buflen]            # write: source; read: C result
```

Read ops are replayed with the destination pre-filled with the sentinel
`0x5A5A5A5A`, so that untouched positions (strided reads, forgiving failures)
are compared meaningfully.
