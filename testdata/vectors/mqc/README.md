# MQ coder (internal/mqc) oracle vectors

`mqc_vectors.json` pins the pure-Go MQ arithmetic coder (`internal/mqc`) to the
OpenJPEG C reference (`oracle/openjpeg`, mqc.c / mqc_inl.h). Every vector was
produced by driving the reference coder and recording its exact bytes/bits.

## Format

A single JSON object with four arrays. Byte fields (`out`, `in`) are lowercase
hex strings; `ctxs`/`bits`/`bpbits` are arrays of small integers.

### `enc` — MQ encoder cases

Both sides initialise with the standard T1 context states
(`opj_mqc_resetstates` + `setstate(UNI,0,46)`, `setstate(AGG,0,3)`,
`setstate(ZC,0,4)` — i.e. `MQC.ResetEnc`).

| field | meaning |
|-------|---------|
| `name` | profile + termination |
| `term` | `flush`, `erterm`, or `segmark` (segmark = `SegmarkEnc` then `Flush`) |
| `ctxs`, `bits` | per-symbol context number (0..18) and decision bit fed to `Encode` |
| `out` | expected coded bytes |
| `numbytes` | expected `opj_mqc_numbytes` |

Profiles: `uniform` (random ctx/bit), `skew90` (90/10 skewed bits),
`ctxswitch` (cycling contexts), `longmps` (long MPS runs on one context).

### `dec` — MQ decoder cases

Round-trip entries (one per `enc` case) plus adversarial byte streams
(`mq-all-ff`, `mq-ff-90`, `mq-single-ff`, `mq-truncated`, `mq-empty`).

| field | meaning |
|-------|---------|
| `in` | input bytes (length `len`) |
| `len` | coded length; caller must supply `len+2` writable bytes |
| `ctxs` | context per decoded symbol |
| `bits` | expected decoded symbols |
| `eobsc` | expected `end_of_byte_stream_counter` after decoding |

For round-trip cases `bits` equals the encoder's input bits (segmark cases
append the 4 marker symbols `1010` on context 18), proving encode∘decode = id.

### `bypass` — RAW/BYPASS encoder cases

Pipeline: `InitEnc`; encode the MQ prefix (`ctxs`/`bits`); `Flush`;
`BypassInitEnc`; `BypassEnc` each of `bpbits`; `BypassFlushEnc(erterm)`.
Covers the 0xff stuffing and the 0xff/0x7f discard rules.

### `raw` — RAW decoder cases

`RawInitDec` on `in` (length `len`), then `count` calls to `RawDecode`,
compared to `bits`. Includes 0xff-heavy, truncated, empty and all-zero inputs.

## Regeneration

Requires the built oracle at `oracle/openjpeg/build/bin/libopenjp2.a`.

```sh
cd oracle/openjpeg
gcc -O2 -I src/lib/openjp2 -I build/src/lib/openjp2 \
    ../harness/w2/gen.c build/bin/libopenjp2.a -lm -o /tmp/genw2
/tmp/genw2 > ../../testdata/vectors/mqc/mqc_vectors.json
```

The harness (`oracle/harness/w2/gen.c`) is gitignored and regenerable. Its
PRNG (xorshift32) is only used to synthesise the C-side inputs; the Go tests
read the recorded arrays directly and do not depend on it.
