"""Regenerate the decoder-path fixtures TestDecoderCostsHold measures: python3 decoders.py DIR.

Each is a 512 x 512 gradient; memory per pixel depends on the decode path, not the content.
The WebP alpha cases quantize alpha to a few levels so libwebp codes it with a palette, the
most expensive WebP path measured.
"""
import sys

from PIL import Image, ImageFile

ImageFile.MAXBLOCK = 1 << 26
out = sys.argv[1]
N = 512
rgb = Image.merge("RGB", [Image.linear_gradient("L").resize((N, N)), Image.linear_gradient("L").rotate(90).resize((N, N)), Image.new("L", (N, N), 128)])


def alpha(levels):
    step = 255 // (levels - 1)
    return Image.linear_gradient("L").resize((N, N)).point(lambda v: (v * levels // 256) * step)


rgb.save(f"{out}/path-ycbcr-progressive.jpg", quality=90, subsampling=0, progressive=True)
rgb.convert("L").save(f"{out}/path-gray-progressive.jpg", quality=90, progressive=True)
rgb.save(f"{out}/path-rgb.jpg", quality=90, subsampling=0, keep_rgb=True)
rgb.save(f"{out}/path-rgb-progressive.jpg", quality=90, subsampling=0, keep_rgb=True, progressive=True)
rgb.convert("CMYK").save(f"{out}/path-cmyk.jpg", quality=90, subsampling=0)
rgb.convert("CMYK").save(f"{out}/path-cmyk-progressive.jpg", quality=90, subsampling=0, progressive=True)
for levels in (2, 16):
    im = rgb.copy()
    im.putalpha(alpha(levels))
    im.save(f"{out}/path-webp-lossy-alpha{levels}.webp", quality=80, alpha_quality=100)
    rgb.quantize(levels).convert("RGB").save(f"{out}/path-webp-palette{levels}.webp", lossless=True)

# Over budget only at WebP's real cost: 5000 x 4600 is 23 megapixels, 276 MB at 12 bytes a
# pixel and 92 MB at 4. Written at the size TestThumbnailRefusesBombs needs.
big = Image.merge("RGB", [Image.linear_gradient("L").resize((5000, 4600)), Image.new("L", (5000, 4600), 90), Image.new("L", (5000, 4600), 160)])
big.putalpha(Image.linear_gradient("L").resize((5000, 4600)).point(lambda v: (v * 16 // 256) * 17))
big.save(f"{out}/webp-alpha-over-budget.webp", quality=50, alpha_quality=100)
