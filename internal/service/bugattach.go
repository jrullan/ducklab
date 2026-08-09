package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bug attachments: the screenshot that says what a paragraph cannot.
//
// A person testing the running app sees a broken layout, a wrong number, a
// dead button — evidence that is visual by nature. Reports were text-only, so
// the evidence stayed on the person's screen and the triage judged a
// description of a picture. Attachments live beside the record
// (.ducklab/bugs/attachments/<bug>/), and a triager with vision is shown the
// images themselves.

// maxAttachmentBytes bounds one attachment (I3). Screenshots compress well;
// a bound this size refuses videos and raw dumps without bothering anyone.
const maxAttachmentBytes = 8 << 20

// attachmentsDir is where one bug's files live.
func attachmentsDir(projectPath, bugID string) string {
	return filepath.Join(projectPath, ".ducklab", "bugs", "attachments", bugID)
}

// BugAttach stores one attachment for a bug and returns the bug's full list.
func (s *Service) BugAttach(ctx context.Context, projectID, bugID, filename string, data []byte) ([]string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	if err := s.bugExists(ctx, projectID, bugID); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("the attachment is empty")
	}
	if len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment too large (%d bytes; the cap is %d) — attach a screenshot, not a recording", len(data), maxAttachmentBytes)
	}
	// The base name only: a filename is a label here, never a path.
	name := filepath.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." {
		return nil, fmt.Errorf("attachment needs a filename")
	}
	dir := attachmentsDir(entry.Path, bugID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return nil, err
	}
	return listAttachments(entry.Path, bugID), nil
}

// BugAttachmentPath resolves an attachment for serving, refusing traversal.
func (s *Service) BugAttachmentPath(ctx context.Context, projectID, bugID, name string) (string, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return "", err
	}
	clean := filepath.Base(name)
	p := filepath.Join(attachmentsDir(entry.Path, bugID), clean)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no attachment %q on %s", clean, bugID)
	}
	return p, nil
}

// listAttachments names a bug's files, sorted for stable display.
func listAttachments(projectPath, bugID string) []string {
	entries, err := os.ReadDir(attachmentsDir(projectPath, bugID))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// bugExists refuses attachments to ghosts.
func (s *Service) bugExists(ctx context.Context, projectID, bugID string) error {
	list, err := s.BugList(ctx, projectID, false)
	if err != nil {
		return err
	}
	for _, b := range list {
		if b.ID == bugID {
			return nil
		}
	}
	return fmt.Errorf("no bug %q in this project", bugID)
}

// attachmentDataURLs loads a bug's IMAGE attachments as data URLs for a
// vision model, bounded so a triage prompt cannot balloon past reason.
func attachmentDataURLs(projectPath, bugID string, maxTotal int) []string {
	var out []string
	total := 0
	for _, name := range listAttachments(projectPath, bugID) {
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if !strings.HasPrefix(ct, "image/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(attachmentsDir(projectPath, bugID), name))
		if err != nil {
			continue
		}
		if total += len(data); total > maxTotal {
			break
		}
		out = append(out, "data:"+ct+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	return out
}
