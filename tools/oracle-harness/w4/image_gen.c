/* W4 oracle harness: dumps opj_image_comp_header_update geometry vectors.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w4/image_gen.c oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread \
 *       -o /tmp/image_gen
 *   /tmp/image_gen > testdata/vectors/image/vectors.json
 *
 * Each case is a single-tile grid (tw=th=1) spanning the full image, so the
 * update reduces to the standard per-component geometry derivation from image
 * bounds, sub-sampling (dx/dy) and reduce factor.
 */
#include <stdio.h>
#include <stdint.h>
#include <string.h>

#include "opj_includes.h"

struct icase {
    OPJ_UINT32 x0, y0, x1, y1;
    OPJ_UINT32 dx, dy;
    OPJ_UINT32 prec;
    OPJ_UINT32 reduce; /* component factor */
};

int main(void)
{
    struct icase cases[] = {
        /* x0 y0   x1    y1    dx dy prec reduce */
        {  0,  0,  256,  256,  1, 1,  8, 0 },
        {  0,  0,  256,  256,  2, 2,  8, 0 },
        {  0,  0,  257,  259,  1, 1,  8, 0 },
        {  0,  0,  257,  259,  2, 2,  8, 0 },
        {  1,  1,  256,  256,  1, 1,  8, 0 },
        {  3,  5,  257,  259,  2, 2,  8, 0 },
        {  0,  0,  256,  256,  1, 1,  8, 1 },
        {  0,  0,  256,  256,  1, 1,  8, 2 },
        {  0,  0,  257,  259,  2, 2,  8, 3 },
        {  7, 11,  640,  480,  4, 4, 12, 2 },
        {  0,  0, 1024, 1024,  1, 1, 16, 5 },
        {  5,  0,  100,  100,  3, 1,  8, 0 },
        {  0,  9,  100,  100,  1, 4,  8, 1 },
        { 13, 17,  999,  777,  4, 2, 10, 3 },
        {  0,  0,    1,    1,  1, 1,  8, 0 },
        {  0,  0,    1,    1,  1, 1,  8, 3 },
    };
    int ncases = (int)(sizeof(cases) / sizeof(cases[0]));

    printf("{\n  \"cases\": [\n");
    for (int c = 0; c < ncases; c++) {
        struct icase *ic = &cases[c];

        opj_image_t image;
        opj_image_comp_t comp;
        opj_cp_t cp;
        memset(&image, 0, sizeof(image));
        memset(&comp, 0, sizeof(comp));
        memset(&cp, 0, sizeof(cp));

        image.x0 = ic->x0;
        image.y0 = ic->y0;
        image.x1 = ic->x1;
        image.y1 = ic->y1;
        image.numcomps = 1;
        image.comps = &comp;

        comp.dx = ic->dx;
        comp.dy = ic->dy;
        comp.prec = ic->prec;
        comp.factor = ic->reduce;

        /* single tile spanning the whole image */
        cp.tx0 = ic->x0;
        cp.ty0 = ic->y0;
        cp.tdx = ic->x1 - ic->x0;
        cp.tdy = ic->y1 - ic->y0;
        cp.tw = 1;
        cp.th = 1;

        opj_image_comp_header_update(&image, &cp);

        printf("    {\"x0\": %u, \"y0\": %u, \"x1\": %u, \"y1\": %u, "
               "\"dx\": %u, \"dy\": %u, \"prec\": %u, \"reduce\": %u, "
               "\"w\": %u, \"h\": %u, \"cx0\": %u, \"cy0\": %u}%s\n",
               ic->x0, ic->y0, ic->x1, ic->y1, ic->dx, ic->dy, ic->prec,
               ic->reduce, comp.w, comp.h, comp.x0, comp.y0,
               (c == ncases - 1) ? "" : ",");
    }
    printf("  ]\n}\n");
    return 0;
}
