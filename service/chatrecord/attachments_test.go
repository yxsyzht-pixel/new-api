package chatrecord

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestExtractsImagesFromEachProtocol(t *testing.T) {
	dataURL := "data:image/png;base64," + onePixelPNG

	cases := []struct {
		name string
		body string
		kind string
	}{
		{
			"chat completions image_url",
			`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + dataURL + `"}}]}]}`,
			"image",
		},
		{
			"responses input_image",
			`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + dataURL + `"}]}]}`,
			"image",
		},
		{
			"claude base64 source",
			`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + onePixelPNG + `"}}]}]}`,
			"image",
		},
		{
			"responses input_file",
			`{"input":[{"role":"user","content":[{"type":"input_file","filename":"report.pdf","file_data":"data:application/pdf;base64,` + onePixelPNG + `"}]}]}`,
			"file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := ExtractAttachments([]byte(tc.body), 1<<20)
			if len(found) != 1 {
				t.Fatalf("found %d attachments, want 1", len(found))
			}
			if found[0].Kind != tc.kind {
				t.Errorf("kind = %q, want %q", found[0].Kind, tc.kind)
			}
			if len(found[0].Data) == 0 {
				t.Error("the bytes were not decoded")
			}
		})
	}
}

// A link is noted, not fetched: reaching out to a caller-supplied URL is not
// something a transcript is worth.
func TestRemoteURLsAreNotedNotFetched(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}]}`
	found := ExtractAttachments([]byte(body), 1<<20)
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	if found[0].SourceURL != "https://example.com/cat.png" {
		t.Errorf("source url = %q", found[0].SourceURL)
	}
	if len(found[0].Data) != 0 {
		t.Error("a remote image was downloaded; it must only be noted")
	}
}

func TestOversizedAttachmentsAreSkipped(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(make([]byte, 4096))
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + big + `"}}]}]}`

	if found := ExtractAttachments([]byte(body), 1024); len(found) != 0 {
		t.Fatalf("found %d attachments past the size limit, want 0", len(found))
	}
	if found := ExtractAttachments([]byte(body), 1<<20); len(found) != 1 {
		t.Fatalf("found %d attachments within the limit, want 1", len(found))
	}
}

func TestSavesUnderTheStaffFolderAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	cfg := operation_setting.GetChatRecordSetting()
	previous := cfg.FileRoot
	cfg.FileRoot = root
	t.Cleanup(func() { cfg.FileRoot = previous })

	when := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	data, _ := base64.StdEncoding.DecodeString(onePixelPNG)
	attachment := Attachment{Kind: "image", MediaType: "image/png", Data: data}

	first := SaveAttachments("A1024", when, []Attachment{attachment})
	if len(first) != 1 || first[0].Path == "" {
		t.Fatalf("nothing was stored: %+v", first)
	}
	if !strings.HasPrefix(first[0].Path, "A1024/2026-08-22/") {
		t.Errorf("path = %q, want it filed under the staff id and day", first[0].Path)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(first[0].Path))); err != nil {
		t.Fatalf("the file is not on disk: %v", err)
	}

	// The same picture in a later turn must not be written twice.
	second := SaveAttachments("A1024", when, []Attachment{attachment})
	if second[0].Path != first[0].Path {
		t.Errorf("identical bytes were stored twice: %q then %q", first[0].Path, second[0].Path)
	}

	entries, err := os.ReadDir(filepath.Join(root, "A1024", "2026-08-22"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d files on disk, want 1", len(entries))
	}
}

// A staff id comes from a key someone typed, so it must not be able to name a
// folder outside the root.
func TestStaffFolderCannotEscapeTheRoot(t *testing.T) {
	for _, staffID := range []string{"../../etc", "..", "/absolute", "a/../../b", ""} {
		folder := sanitizeFolder(staffID)
		if strings.ContainsAny(folder, `/\.`) {
			t.Errorf("sanitizeFolder(%q) = %q, which can still traverse", staffID, folder)
		}
		if folder == "" {
			t.Errorf("sanitizeFolder(%q) produced an empty folder", staffID)
		}
	}
}

func TestResolveStoredPathRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	cfg := operation_setting.GetChatRecordSetting()
	previous := cfg.FileRoot
	cfg.FileRoot = root
	t.Cleanup(func() { cfg.FileRoot = previous })

	if _, err := ResolveStoredPath("A1024/2026-08-22/abc.png"); err != nil {
		t.Fatalf("a normal path was refused: %v", err)
	}
	for _, bad := range []string{"../../etc/passwd", "A1024/../../../etc/passwd", "/etc/passwd"} {
		if _, err := ResolveStoredPath(bad); err == nil {
			t.Errorf("ResolveStoredPath(%q) was allowed out of the root", bad)
		}
	}
}
