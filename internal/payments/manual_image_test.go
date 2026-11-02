package payments

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// encodePNG is a picture the validator accepts.
func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return b.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return b.Bytes()
}

// The manual payment method takes a picture of the operator's requisites, not any
// file: PNG or JPEG, bounded in bytes and in pixels. Each refusal is a coded error,
// which is what the panel shows the operator in their own language.
func TestCheckManualPayImage(t *testing.T) {
	t.Parallel()
	for _, b := range [][]byte{encodePNG(t, 64, 64), encodeJPEG(t, 64, 64)} {
		if err := CheckManualPayImage(b); err != nil {
			t.Fatalf("%s refused: %v", ManualPayImageContentType(b), err)
		}
	}
	// A picture at the pixel limit is still a picture: the cap is on what exceeds it.
	if err := CheckManualPayImage(encodePNG(t, maxManualImageDim, 8)); err != nil {
		t.Errorf("a picture at the dimension limit: %v", err)
	}
	for _, tc := range []struct {
		name string
		b    []byte
		code string
	}{
		{"empty", nil, "err.emptyFile"},
		{"not an image", []byte("card 0000 0000"), "err.needPngJpeg"},
		{"too heavy", append(encodePNG(t, 32, 32), make([]byte, MaxManualImageBytes)...), "err.imageTooBig"},
		{"too many pixels", encodePNG(t, maxManualImageDim+1, 8), "err.imageTooLarge"},
	} {
		err := CheckManualPayImage(tc.b)
		var fe *model.FieldError
		if !errors.As(err, &fe) || fe.Code != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
}

// ReadManualPayImage is what the upload handler reads through: a valid picture comes
// back whole, and an oversized one is refused rather than buffered.
func TestReadManualPayImage(t *testing.T) {
	t.Parallel()
	png := encodePNG(t, 40, 40)
	got, err := ReadManualPayImage(bytes.NewReader(png))
	if err != nil || !bytes.Equal(got, png) {
		t.Fatalf("read: %d bytes, %v", len(got), err)
	}
	big := append(encodePNG(t, 32, 32), make([]byte, MaxManualImageBytes)...)
	if _, err := ReadManualPayImage(bytes.NewReader(big)); err == nil {
		t.Error("an oversized upload was accepted")
	}
}

// The type and the Telegram filename follow the bytes: a JPEG must not be announced as
// a PNG, or Telegram renders a document instead of a photo.
func TestManualPayImageTypeAndFilename(t *testing.T) {
	t.Parallel()
	jpg := encodeJPEG(t, 16, 16)
	if ct := ManualPayImageContentType(jpg); ct != "image/jpeg" {
		t.Errorf("jpeg content type = %q", ct)
	}
	if name := ManualPayImageFilename(jpg); name != "requisites.jpg" {
		t.Errorf("jpeg filename = %q", name)
	}
	png := encodePNG(t, 16, 16)
	if ct := ManualPayImageContentType(png); ct != "image/png" {
		t.Errorf("png content type = %q", ct)
	}
	if name := ManualPayImageFilename(png); name != "requisites.png" {
		t.Errorf("png filename = %q", name)
	}
}
