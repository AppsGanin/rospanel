package payments

import (
	"bytes"
	"image"
	// The two formats an operator's QR code or screenshot actually comes in; the
	// decoders are what DecodeConfig below reads the dimensions through.
	_ "image/jpeg"
	_ "image/png"
	"io"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The manual payment method's picture: the operator's own QR code, a screenshot of
// their card or wallet page — whatever they would otherwise have to describe in text.
// PNG or JPEG, bounded so neither the subscription page nor a Telegram upload carries
// a print-resolution scan.
const (
	MaxManualImageBytes = 1 << 20 // 1 MiB: a full-screen phone screenshot fits
	maxManualImageDim   = 2048    // the longest side, in pixels
)

// CheckManualPayImage validates an uploaded picture. The bytes are stored as they
// came: re-encoding would be the only way to normalize them, and it would also be the
// only way to strip metadata — not worth it for an image the operator chose.
func CheckManualPayImage(b []byte) error {
	if len(b) == 0 {
		return model.FieldErr("err.emptyFile", "пустой файл")
	}
	if len(b) > MaxManualImageBytes {
		return model.FieldErr("err.imageTooBig", "картинка больше {{kb}} КБ",
			map[string]any{"kb": MaxManualImageBytes >> 10})
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "png" && format != "jpeg") {
		return model.FieldErr("err.needPngJpeg", "нужен PNG или JPEG")
	}
	if cfg.Width > maxManualImageDim || cfg.Height > maxManualImageDim {
		return model.FieldErr("err.imageTooLarge", "изображение больше {{dim}}×{{dim}} пикселей",
			map[string]any{"dim": maxManualImageDim})
	}
	return nil
}

// ManualPayImageContentType is the MIME type of a picture CheckManualPayImage
// accepted. Anything unrecognised reports PNG — the callers only ever hold validated
// bytes.
func ManualPayImageContentType(b []byte) string {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 {
		return "image/jpeg"
	}
	return "image/png"
}

// ManualPayImageFilename names the picture for a Telegram upload: the extension is
// cosmetic there, but a "photo" without one reads as a document in some clients.
func ManualPayImageFilename(b []byte) string {
	if ManualPayImageContentType(b) == "image/jpeg" {
		return "requisites.jpg"
	}
	return "requisites.png"
}

// ReadManualPayImage reads an upload, refusing one that is too big before buffering
// all of it.
func ReadManualPayImage(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxManualImageBytes+1))
	if err != nil {
		return nil, err
	}
	if err := CheckManualPayImage(b); err != nil {
		return nil, err
	}
	return b, nil
}
