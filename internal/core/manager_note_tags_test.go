package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A note or tag change lands in the user's journal — once per real change, never
// for a re-save of what was already there — and the note's text stays out of it.
func TestNoteAndTagsAreJournalledWithoutLeakingTheNote(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "annotated", 0, 0)

	if err := m.SetUserNote(ctx, u.ID, "  pays late, remind on the 5th \r\n"); err != nil {
		t.Fatalf("note: %v", err)
	}
	if err := m.SetUserNote(ctx, u.ID, "pays late, remind on the 5th"); err != nil {
		t.Fatalf("same note: %v", err)
	}
	if err := m.SetUserTags(ctx, u.ID, []string{"VIP", "vip", "reseller-a"}); err != nil {
		t.Fatalf("tags: %v", err)
	}
	if err := m.SetUserTags(ctx, u.ID, []string{"reseller-a", "vip"}); err != nil {
		t.Fatalf("same tags: %v", err)
	}

	got, err := m.store.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "pays late, remind on the 5th" {
		t.Errorf("note not trimmed/normalised: %q", got.Note)
	}
	if strings.Join(got.Tags, ",") != "reseller-a,vip" {
		t.Errorf("tags: %v", got.Tags)
	}

	events := trail(t, m, u.ID)
	notes, tags := 0, 0
	for _, e := range events {
		switch e.Action {
		case model.EventUserNote:
			notes++
			if strings.Contains(fmt.Sprint(e.Details), "pays late") {
				t.Errorf("the note text leaked into the journal: %v", e.Details)
			}
		case model.EventUserTags:
			tags++
			if !strings.Contains(fmt.Sprint(e.Details), "reseller-a") {
				t.Errorf("the tag row should carry the new list: %v", e.Details)
			}
		}
	}
	if notes != 1 || tags != 1 {
		t.Errorf("want one row each for a change and none for a re-save, got notes=%d tags=%d in %v",
			notes, tags, actions(events))
	}
}

func TestNoteAndTagsRejectWhatCannotBeStored(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "annotated", 0, 0)

	if err := m.SetUserNote(ctx, u.ID, strings.Repeat("x", model.MaxUserNoteLen+1)); err == nil {
		t.Error("an over-long note was accepted")
	}
	if err := m.SetUserNote(ctx, u.ID, strings.Repeat("я", model.MaxUserNoteLen)); err != nil {
		t.Errorf("the limit is in characters, not bytes: %v", err)
	}
	if err := m.SetUserTags(ctx, u.ID, []string{"a,b"}); err == nil {
		t.Error("a tag with a comma was accepted")
	}
	var ve *ValidationError
	if err := m.SetUserTags(ctx, u.ID, []string{strings.Repeat("x", model.MaxUserTagLen+1)}); err == nil {
		t.Error("an over-long tag was accepted")
	} else if !errors.As(err, &ve) || ve.Code != "err.tagsInvalid" {
		t.Errorf("want a coded validation error, got %v", err)
	}
	if got, _ := m.store.GetUser(u.ID); len(got.Tags) != 0 {
		t.Errorf("a refused list must not be stored: %v", got.Tags)
	}
}
