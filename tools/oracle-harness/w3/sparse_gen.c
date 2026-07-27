/* W3 oracle harness: generates sparse_array vectors from libopenjp2.
 *
 * Build (from repo root):
 *   gcc -O2 tools/oracle-harness/w3/sparse_gen.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread \
 *       -o /tmp/sparse_gen
 *   /tmp/sparse_gen testdata/vectors/sparse/vectors.bin
 *
 * Vector format (little-endian) is documented in
 * testdata/vectors/sparse/README.md.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef int32_t OPJ_INT32;
typedef uint32_t OPJ_UINT32;
typedef int OPJ_BOOL;
typedef struct opj_sparse_array_int32 opj_sparse_array_int32_t;

extern opj_sparse_array_int32_t* opj_sparse_array_int32_create(
    OPJ_UINT32 width, OPJ_UINT32 height, OPJ_UINT32 block_width,
    OPJ_UINT32 block_height);
extern void opj_sparse_array_int32_free(opj_sparse_array_int32_t* sa);
extern OPJ_BOOL opj_sparse_array_int32_read(const opj_sparse_array_int32_t* sa,
        OPJ_UINT32 x0, OPJ_UINT32 y0, OPJ_UINT32 x1, OPJ_UINT32 y1,
        OPJ_INT32* dest, OPJ_UINT32 dest_col_stride,
        OPJ_UINT32 dest_line_stride, OPJ_BOOL forgiving);
extern OPJ_BOOL opj_sparse_array_int32_write(opj_sparse_array_int32_t* sa,
        OPJ_UINT32 x0, OPJ_UINT32 y0, OPJ_UINT32 x1, OPJ_UINT32 y1,
        const OPJ_INT32* src, OPJ_UINT32 src_col_stride,
        OPJ_UINT32 src_line_stride, OPJ_BOOL forgiving);

static FILE* g_out;

static uint32_t g_seed = 0x12345678u;
static uint32_t lcg(void)
{
    g_seed = g_seed * 1103515245u + 12345u;
    return g_seed;
}

static void wu32(uint32_t v) { fwrite(&v, 4, 1, g_out); }
static void wi32(int32_t v) { fwrite(&v, 4, 1, g_out); }

#define OP_WRITE 0
#define OP_READ 1
#define OP_CREATE 2
#define SENTINEL 0x5A5A5A5A

/* Emit a create op so the Go replay knows to (re)allocate a fresh array. */
static void do_create(uint32_t w, uint32_t h, uint32_t bw, uint32_t bh)
{
    wu32(OP_CREATE);
    wu32(w); wu32(h); wu32(bw); wu32(bh);
}

/* Emit a write op: fill src[] with deterministic values, dump params + src. */
static void do_write(opj_sparse_array_int32_t* sa,
                     uint32_t x0, uint32_t y0, uint32_t x1, uint32_t y1,
                     uint32_t col_stride, uint32_t line_stride,
                     uint32_t forgiving)
{
    uint32_t nx = (x1 > x0) ? (x1 - x0) : 0;
    uint32_t ny = (y1 > y0) ? (y1 - y0) : 0;
    /* buffer must span (ny-1)*line + (nx-1)*col + 1, allocate generously */
    uint32_t span = 1;
    if (nx && ny) {
        span = (ny - 1) * line_stride + (nx - 1) * col_stride + 1;
    }
    uint32_t buflen = span;
    int32_t* buf = (int32_t*)calloc(buflen ? buflen : 1, sizeof(int32_t));
    for (uint32_t i = 0; i < buflen; i++) {
        buf[i] = (int32_t)lcg();
    }
    OPJ_BOOL ret = opj_sparse_array_int32_write(sa, x0, y0, x1, y1, buf,
                   col_stride, line_stride, forgiving);
    wu32(OP_WRITE);
    wu32(x0); wu32(y0); wu32(x1); wu32(y1);
    wu32(col_stride); wu32(line_stride); wu32(forgiving);
    wu32((uint32_t)ret);
    wu32(buflen);
    fwrite(buf, sizeof(int32_t), buflen, g_out);
    free(buf);
}

/* Emit a read op: prefill dest[] with SENTINEL, dump params + result. */
static void do_read(opj_sparse_array_int32_t* sa,
                    uint32_t x0, uint32_t y0, uint32_t x1, uint32_t y1,
                    uint32_t col_stride, uint32_t line_stride,
                    uint32_t forgiving)
{
    uint32_t nx = (x1 > x0) ? (x1 - x0) : 0;
    uint32_t ny = (y1 > y0) ? (y1 - y0) : 0;
    uint32_t span = 1;
    if (nx && ny) {
        span = (ny - 1) * line_stride + (nx - 1) * col_stride + 1;
    }
    uint32_t buflen = span;
    int32_t* buf = (int32_t*)malloc((buflen ? buflen : 1) * sizeof(int32_t));
    for (uint32_t i = 0; i < buflen; i++) {
        buf[i] = SENTINEL;
    }
    OPJ_BOOL ret = opj_sparse_array_int32_read(sa, x0, y0, x1, y1, buf,
                   col_stride, line_stride, forgiving);
    wu32(OP_READ);
    wu32(x0); wu32(y0); wu32(x1); wu32(y1);
    wu32(col_stride); wu32(line_stride); wu32(forgiving);
    wu32((uint32_t)ret);
    wu32(buflen);
    fwrite(buf, sizeof(int32_t), buflen, g_out);
    free(buf);
}

/* A test case begins with its 4 create params + op count placeholder. */
typedef struct {
    uint32_t w, h, bw, bh;
} cfg_t;

int main(int argc, char** argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage: %s out.bin\n", argv[0]);
        return 1;
    }
    g_out = fopen(argv[1], "wb");
    if (!g_out) {
        perror("fopen");
        return 1;
    }

    cfg_t cfgs[] = {
        {1, 1, 1, 1},
        {7, 5, 4, 4},
        {16, 16, 4, 4},
        {13, 9, 8, 8},
        {5, 5, 2, 3},
        {33, 17, 64, 64},   /* block larger than dims */
        {64, 64, 64, 64},
        {100, 80, 16, 16},
        {37, 61, 8, 16},
        {256, 3, 64, 64},
        {3, 256, 64, 64},
    };
    uint32_t ncfg = sizeof(cfgs) / sizeof(cfgs[0]);
    wu32(ncfg);

    for (uint32_t ci = 0; ci < ncfg; ci++) {
        cfg_t c = cfgs[ci];
        wu32(c.w); wu32(c.h); wu32(c.bw); wu32(c.bh);

        /* Count ops we will emit; we mirror the exact same sequence below. */
        /* We emit ops to a memory position placeholder, so pre-count. */
        long count_pos = ftell(g_out);
        wu32(0); /* op count placeholder */

        opj_sparse_array_int32_t* sa =
            opj_sparse_array_int32_create(c.w, c.h, c.bw, c.bh);
        uint32_t nops = 0;

        int small = (c.w * c.h) <= 256;
        if (sa) {
            do_create(c.w, c.h, c.bw, c.bh); nops++;
            /* 1: full write, col stride 1 */
            do_write(sa, 0, 0, c.w, c.h, 1, c.w, 1); nops++;
            /* 2: full read back, col stride 1 */
            do_read(sa, 0, 0, c.w, c.h, 1, c.w, 1); nops++;
            /* Larger/padded strides only for small arrays to keep vectors tiny */
            if (small) {
                /* 3: full read, col stride 2 (padded line stride) */
                do_read(sa, 0, 0, c.w, c.h, 2, 2 * c.w + 3, 1); nops++;
                /* 4: full read, col stride 3 */
                do_read(sa, 0, 0, c.w, c.h, 3, 3 * c.w, 1); nops++;
                /* 5: full read, col stride 8 */
                do_read(sa, 0, 0, c.w, c.h, 8, 8 * c.w, 1); nops++;
            }

            /* 6..: sub-region reads */
            if (c.w >= 3 && c.h >= 2) {
                do_read(sa, 1, 0, c.w - 1, c.h, 1, c.w, 1); nops++;
                if (small) {
                    do_read(sa, 1, 1, c.w - 1, c.h - 1, 2, 2 * c.w, 1); nops++;
                }
                /* single pixel */
                do_read(sa, 2, 1, 3, 2, 1, 1, 1); nops++;
                /* single row */
                do_read(sa, 0, 1, c.w, 2, 4, 4 * c.w, 1); nops++;
                /* single column */
                do_read(sa, 1, 0, 2, c.h, 1, 1, 1); nops++;
            }

            /* invalid-region reads: forgiving true and false */
            do_read(sa, 0, 0, c.w + 5, c.h, 1, c.w + 5, 1); nops++;
            do_read(sa, 0, 0, c.w + 5, c.h, 1, c.w + 5, 0); nops++;
            do_read(sa, c.w, 0, c.w + 1, c.h, 1, 1, 1); nops++; /* x0>=width */
            do_write(sa, 0, 0, 0, c.h, 1, c.w, 1); nops++;      /* x1<=x0 */
            do_write(sa, 0, 0, c.w, c.h + 3, 1, c.w, 0); nops++; /* y1>height, strict */

            opj_sparse_array_int32_free(sa);
        }

        /* A second array where only some blocks are written, leaving holes */
        opj_sparse_array_int32_t* sa2 =
            opj_sparse_array_int32_create(c.w, c.h, c.bw, c.bh);
        if (sa2) {
            do_create(c.w, c.h, c.bw, c.bh); nops++;
            /* write only a small sub-rectangle */
            uint32_t wx1 = (c.w >= 4) ? 4 : c.w;
            uint32_t wy1 = (c.h >= 4) ? 4 : c.h;
            do_write(sa2, 0, 0, wx1, wy1, 1, wx1, 1); nops++;
            /* strided write into another region */
            if (c.w >= 6 && c.h >= 6) {
                do_write(sa2, 2, 2, 6, 6, 2, 2 * 4, 1); nops++;
            }
            /* read whole array: mix of written + nil (zero) blocks */
            do_read(sa2, 0, 0, c.w, c.h, 1, c.w, 1); nops++;
            if (small) {
                do_read(sa2, 0, 0, c.w, c.h, 2, 2 * c.w, 1); nops++;
            }
            opj_sparse_array_int32_free(sa2);
        }

        long here = ftell(g_out);
        fseek(g_out, count_pos, SEEK_SET);
        wu32(nops);
        fseek(g_out, here, SEEK_SET);
    }

    fclose(g_out);
    fprintf(stderr, "wrote %s\n", argv[1]);
    return 0;
}
