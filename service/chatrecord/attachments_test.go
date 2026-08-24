package chatrecord

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Two workers can be storing the same picture at the same moment while the
// serving endpoint reads it. A reader must see the whole file or nothing.
func TestConcurrentSavesLeaveOneCompleteFile(t *testing.T) {
	root := t.TempDir()
	cfg := operation_setting.GetChatRecordSetting()
	previous := cfg.FileRoot
	cfg.FileRoot = root
	t.Cleanup(func() { cfg.FileRoot = previous })

	when := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	payload := make([]byte, 512*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	attachment := Attachment{Kind: "image", MediaType: "image/png", Data: payload}

	var wg sync.WaitGroup
	paths := make([]string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			if stored := SaveAttachments("A1024", when, []Attachment{attachment}); len(stored) == 1 {
				paths[slot] = stored[slot*0].Path
			}
		}(i)
	}
	wg.Wait()

	for i, path := range paths {
		if path == "" {
			t.Fatalf("worker %d stored nothing", i)
		}
		if path != paths[0] {
			t.Fatalf("worker %d wrote to %q, worker 0 to %q", i, path, paths[0])
		}
	}

	// Exactly one file, complete, and no temporary left behind.
	dir := filepath.Join(root, "A1024", "2026-08-22")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("%d files left in the folder: %v", len(entries), names)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("the stored file is %d bytes, want %d", len(onDisk), len(payload))
	}
}

// A turn replays the whole conversation. Walking all of it would decode and
// re-record every picture ever sent, once per turn — a fifty-turn conversation
// with one image would write fifty rows pointing at the same file.
func TestOnlyTheNewestMessagesAttachmentsAreTaken(t *testing.T) {
	old := "data:image/png;base64," + onePixelPNG
	// A different picture, so the two are distinguishable.
	other := "data:image/gif;base64," + onePixelPNG

	body := `{"messages":[
	  {"role":"user","content":[{"type":"image_url","image_url":{"url":"` + old + `"}}]},
	  {"role":"assistant","content":"got it"},
	  {"role":"user","content":[{"type":"image_url","image_url":{"url":"` + other + `"}}]}
	]}`

	found := ExtractAttachments([]byte(body), 1<<20)
	if len(found) != 1 {
		t.Fatalf("found %d attachments, want only the newest message's", len(found))
	}
	if found[0].MediaType != "image/gif" {
		t.Errorf("media type = %q, want the newest message's image/gif", found[0].MediaType)
	}
}

// The image endpoints are exempt: what goes in and out of them is a picture by
// definition, and keeping every one fills a disk with pixels the recorded
// prompt already accounts for.
func TestImageRoutesKeepNoAttachments(t *testing.T) {
	exempt := []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/images/variations",
	}
	for _, endpoint := range exempt {
		if StoreAttachmentsFor(endpoint) {
			t.Errorf("%s must not have its pictures kept", endpoint)
		}
	}

	kept := []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/messages",
	}
	for _, endpoint := range kept {
		if !StoreAttachmentsFor(endpoint) {
			t.Errorf("%s carries attachments worth keeping", endpoint)
		}
	}
}

// The exemption has to hold where it counts: nothing on disk, no rows.
func TestNothingIsStoredForAnImageRoute(t *testing.T) {
	root := t.TempDir()
	cfg := operation_setting.GetChatRecordSetting()
	previous := cfg.FileRoot
	cfg.FileRoot = root
	t.Cleanup(func() { cfg.FileRoot = previous })

	body := []byte(`{"prompt":"a beach","image":"data:image/png;base64,` + onePixelPNG + `"}`)

	var stored []StoredAttachment
	if StoreAttachmentsFor("/v1/images/edits") {
		stored = SaveAttachments("A1024", time.Now(), ExtractAttachments(body, 1<<20))
	}
	if len(stored) != 0 {
		t.Fatalf("stored %d attachments for an image route, want none", len(stored))
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		t.Fatalf("%d entries were written under the attachment root", len(entries))
	}
}

// What the model produced has to be picked out of the reply, which arrives in
// two quite different shapes, and be labelled as the model's — otherwise a
// picture in the table says nothing about whether somebody sent it or was
// answered with it.
func TestGeneratedImagesAreFoundInEveryReplyShape(t *testing.T) {
	pixel := base64.StdEncoding.EncodeToString([]byte("\x89PNG-not-really"))

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			// The Responses built-in tool, which is how this gateway draws.
			"responses image_generation_call",
			`{"output":[{"type":"image_generation_call","result":"` + pixel + `"}]}`,
		},
		{
			"responses output_image",
			`{"output":[{"type":"message","content":[
			   {"type":"output_image","image_url":"data:image/png;base64,` + pixel + `"}]}]}`,
		},
		{
			// Some providers hang the picture off the message rather than
			// putting it in the content.
			"chat completions images",
			`{"choices":[{"message":{"role":"assistant","images":[
			   {"image_url":{"url":"data:image/png;base64,` + pixel + `"}}]}}]}`,
		},
		{
			"claude content block",
			`{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + pixel + `"}}]}`,
		},
		{
			// A stream: not JSON at all, and the document only appears inside
			// one of its events.
			"streamed response.completed",
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"画好了\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[" +
				"{\"type\":\"image_generation_call\",\"result\":\"" + pixel + "\"}]}}\n\n" +
				"data: [DONE]\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := ExtractReplyAttachments([]byte(tc.body), 1<<20)
			if len(found) != 1 {
				t.Fatalf("found %d attachments, want 1", len(found))
			}
			if found[0].Origin != OriginReply {
				t.Errorf("origin = %q, want %q", found[0].Origin, OriginReply)
			}
			if found[0].Kind != "image" {
				t.Errorf("kind = %q", found[0].Kind)
			}
			if len(found[0].Data) == 0 {
				t.Error("the picture itself was not decoded")
			}
		})
	}
}

// A stream restates the response as it completes, so the same picture arrives
// several times. Decoding it once and leaving the database to reject the rest
// is work nobody needs.
func TestARepeatedPictureInAStreamIsCountedOnce(t *testing.T) {
	pixel := base64.StdEncoding.EncodeToString([]byte("\x89PNG-not-really"))
	event := "data: {\"type\":\"response.output_item.done\",\"item\":" +
		"{\"type\":\"image_generation_call\",\"result\":\"" + pixel + "\"}}\n\n"
	stream := event + event +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[" +
		"{\"type\":\"image_generation_call\",\"result\":\"" + pixel + "\"}]}}\n"

	found := ExtractReplyAttachments([]byte(stream), 1<<20)
	if len(found) != 1 {
		t.Fatalf("the same picture was collected %d times", len(found))
	}
}

// A reply with nothing in it must not invent an attachment, and a body that is
// not JSON at all — the transcript of an audio route, say — must not have
// bytes read out of it as a picture.
func TestAPlainReplyCarriesNoAttachments(t *testing.T) {
	for _, body := range []string{
		"",
		`{"choices":[{"message":{"role":"assistant","content":"就这样"}}]}`,
		"------form-boundary\r\nnot json at all\r\n------form-boundary--",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"没有图\"}\n",
	} {
		if found := ExtractReplyAttachments([]byte(body), 1<<20); len(found) != 0 {
			t.Errorf("%.30q produced %d attachments", body, len(found))
		}
	}
}

// The question's attachments keep saying they came with the question.
func TestAttachmentsSentWithAQuestionAreLabelledAsSuch(t *testing.T) {
	pixel := base64.StdEncoding.EncodeToString([]byte("\x89PNG-not-really"))
	body := `{"messages":[{"role":"user","content":[
	  {"type":"text","text":"这是什么"},
	  {"type":"image_url","image_url":{"url":"data:image/png;base64,` + pixel + `"}}]}]}`

	found := ExtractAttachments([]byte(body), 1<<20)
	if len(found) != 1 {
		t.Fatalf("found %d attachments, want 1", len(found))
	}
	if found[0].Origin != OriginPrompt {
		t.Errorf("origin = %q, want %q", found[0].Origin, OriginPrompt)
	}
}
