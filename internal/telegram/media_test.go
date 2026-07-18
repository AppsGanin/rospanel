package telegram

import "testing"

// The file_id picked here is what a broadcast re-sends to every recipient after one
// upload, so picking the wrong rendition means shipping thumbnails to the audience.
func TestMediaFileID(t *testing.T) {
	if got := (*Message)(nil).MediaFileID(); got != "" {
		t.Errorf("nil message = %q, want empty", got)
	}
	if got := (&Message{Text: "нет вложения"}).MediaFileID(); got != "" {
		t.Errorf("text-only message = %q, want empty", got)
	}

	doc := &Message{Document: &Document{FileID: "doc-1"}}
	if got := doc.MediaFileID(); got != "doc-1" {
		t.Errorf("document = %q, want doc-1", got)
	}

	// Telegram returns every rendition it made; the largest is the one worth
	// re-sending, and it is not promised to arrive last.
	photo := &Message{Photo: []PhotoSize{
		{FileID: "thumb", Width: 90, Height: 90},
		{FileID: "full", Width: 1280, Height: 960},
		{FileID: "mid", Width: 320, Height: 240},
	}}
	if got := photo.MediaFileID(); got != "full" {
		t.Errorf("photo = %q, want full", got)
	}
}
