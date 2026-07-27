# W6 pi + t2 oracle harnesses

Generate the checked-in vectors used by `internal/pi` and `internal/t2` tests.
Regenerable via `tools/oracle-harness/regen.sh`; only the `oracle/` checkout they
build against is gitignored.

## Build & run (from the repo root)

```sh
gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    tools/oracle-harness/w6/pi_harness.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6pi
/tmp/w6pi testdata/vectors/pi/pi_vectors.txt

gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
    -I oracle/openjpeg/build/src/lib/openjp2 \
    tools/oracle-harness/w6/t2_harness.c \
    oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6t2
/tmp/w6t2 testdata/vectors/t2/t2_vectors.txt
```

## How they link

Both harnesses `#include "opj_includes.h"` and call the **non-static** pi.c / t2.c
entry points (`opj_pi_create_decode`, `opj_pi_next`, `opj_t2_encode_packets`,
`opj_t2_decode_packets`, …), which are resolved from `libopenjp2.a`. They build
the `opj_image_t` / `opj_cp_t` / `opj_tcd_tile_t` structures by hand. Because
`opj_includes.h` poisons the libc allocators, harness code uses `opj_malloc` /
`opj_calloc` / `opj_free`.

See `testdata/vectors/{pi,t2}/README.md` for the emitted vector formats.
