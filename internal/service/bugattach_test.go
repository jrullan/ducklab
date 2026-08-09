package service

import (
	"context"
	"strings"
	"testing"
)

// The screenshot that says what a paragraph cannot: stored beside the record,
// listed on the bug, refused for ghosts and for path games.
func TestBugAttachmentsAreStoredListedAndGuarded(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.BugAdd(context.Background(), p.ID, BugRequest{Title: "layout broken on mobile"})
	if err != nil {
		t.Fatal(err)
	}

	png := []byte("\x89PNG fake bytes")
	items, err := s.BugAttach(context.Background(), p.ID, b.ID, "../../../etc/shot.png", png)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0] != "shot.png" {
		t.Fatalf("items = %v — the filename must be a label, never a path", items)
	}

	// Served back, path-safe.
	path, err := s.BugAttachmentPath(context.Background(), p.ID, b.ID, "shot.png")
	if err != nil || !strings.Contains(path, b.ID) {
		t.Fatalf("path = %q, %v", path, err)
	}
	if _, err := s.BugAttachmentPath(context.Background(), p.ID, b.ID, "nope.png"); err == nil {
		t.Error("a missing attachment was served")
	}

	// Listed on the bug itself.
	list, err := s.BugList(context.Background(), p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Attachments) != 1 || list[0].Attachments[0] != "shot.png" {
		t.Errorf("bug list does not carry the attachment: %+v", list)
	}

	// Ghosts get nothing.
	if _, err := s.BugAttach(context.Background(), p.ID, "B-999", "x.png", png); err == nil {
		t.Error("an attachment was accepted for a bug that does not exist")
	}

	// A vision triager gets the image as a data URL; junk files do not ride.
	if _, err := s.BugAttach(context.Background(), p.ID, b.ID, "notes.txt", []byte("text")); err != nil {
		t.Fatal(err)
	}
	urls := attachmentDataURLs(dir, b.ID, 6<<20)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("data URLs = %d (%.40s…) — only images, as data URLs", len(urls), strings.Join(urls, ","))
	}
}
