package chatrecord

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/tidwall/gjson"
)

// Attachments are the images and documents a caller sent along with a message.
// They are pulled out of the request body on a worker, never on the request
// path, and only the path they were written to goes in the database.

// Origin says which half of the exchange a file came with. It travels with the
// attachment rather than being decided where the row is written, so the two can
// never drift apart.
const (
	OriginPrompt = "prompt" // attached to the question
	OriginReply  = "reply"  // produced in the answer
)

type Attachment struct {
	Kind      string // "image" or "file"
	Origin    string // OriginPrompt or OriginReply
	MediaType string
	FileName  string
	Data      []byte
	SourceURL string // set instead of Data when the caller passed a link
}

// StoredAttachment is what ended up on disk.
type StoredAttachment struct {
	Attachment
	SHA256 string
	Size   int64
	Path   string // relative to the configured root
}

var dataURLPattern = regexp.MustCompile(`^data:([^;,]+)(;[^,]*)?,(.*)$`)

// ExtractAttachments walks the shapes each protocol uses for attached content.
// Anything it does not recognise is simply not recorded — a transcript missing
// a file is a smaller problem than a worker tripping over an unfamiliar body.
func ExtractAttachments(body []byte, limit int) []Attachment {
	if len(body) == 0 {
		return nil
	}

	var found []Attachment
	add := func(a Attachment) {
		if len(found) >= 32 {
			return
		}
		if len(a.Data) > limit {
			return
		}
		if len(a.Data) == 0 && a.SourceURL == "" {
			return
		}
		a.Origin = OriginPrompt
		found = append(found, a)
	}

	walkContent := func(content gjson.Result) {
		content.ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			// Chat Completions
			case "image_url":
				add(fromURLOrData("image", part.Get("image_url.url").String(), ""))
			case "input_audio":
				add(fromBase64("file", part.Get("input_audio.data").String(),
					"audio/"+part.Get("input_audio.format").String(), ""))
			case "file":
				add(fromURLOrData("file", part.Get("file.file_data").String(),
					part.Get("file.filename").String()))

			// Responses
			case "input_image":
				add(fromURLOrData("image", part.Get("image_url").String(), ""))
			case "input_file":
				add(fromURLOrData("file", firstNonEmpty(
					part.Get("file_data").String(),
					part.Get("file_url").String(),
				), part.Get("filename").String()))

			// Claude Messages
			case "image", "document":
				kind := "image"
				if part.Get("type").String() == "document" {
					kind = "file"
				}
				source := part.Get("source")
				switch source.Get("type").String() {
				case "base64":
					add(fromBase64(kind, source.Get("data").String(),
						source.Get("media_type").String(), ""))
				case "url":
					add(Attachment{Kind: kind, SourceURL: source.Get("url").String()})
				}
			}
			return true
		})
	}

	// Only the newest user message, for the same reason the transcript keeps
	// only that one: a turn replays the whole conversation, so walking all of
	// it would decode and re-record every picture ever sent, once per turn.
	eachUserContentNewestFirst(body, func(content gjson.Result) bool {
		if content.IsArray() {
			walkContent(content)
		}
		return false
	})

	return found
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// fromURLOrData takes either a data: URL to decode or a link to note down.
func fromURLOrData(kind, value, fileName string) Attachment {
	value = strings.TrimSpace(value)
	if value == "" {
		return Attachment{}
	}
	if matches := dataURLPattern.FindStringSubmatch(value); matches != nil {
		mediaType := matches[1]
		if !strings.Contains(matches[2], "base64") {
			// Percent-encoded data URLs are rare enough to leave alone.
			return Attachment{}
		}
		return fromBase64(kind, matches[3], mediaType, fileName)
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return Attachment{Kind: kind, FileName: fileName, SourceURL: value}
	}
	// A bare base64 blob, which some callers send for files.
	return fromBase64(kind, value, "", fileName)
}

func fromBase64(kind, encoded, mediaType, fileName string) Attachment {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return Attachment{}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(encoded); err != nil {
			return Attachment{}
		}
	}
	return Attachment{Kind: kind, MediaType: mediaType, FileName: fileName, Data: data}
}

// SaveAttachments writes what was found under the staff id of the key that sent
// it, so one person's files sit together. Identical bytes are stored once: the
// same picture sent in twenty turns is one file on disk.
func SaveAttachments(staffID string, when time.Time, attachments []Attachment) []StoredAttachment {
	if len(attachments) == 0 {
		return nil
	}
	cfg := operation_setting.GetChatRecordSetting()
	root := cfg.FileRootOrDefault()

	folder := sanitizeFolder(staffID)
	day := when.Format("2006-01-02")
	dir := filepath.Join(root, folder, day)

	var stored []StoredAttachment
	for _, attachment := range attachments {
		if len(attachment.Data) == 0 {
			// A link, not bytes: remember where it pointed without fetching it.
			// Reaching out to a caller-supplied URL from the gateway is not
			// something a transcript is worth.
			stored = append(stored, StoredAttachment{Attachment: attachment})
			continue
		}
		sum := sha256.Sum256(attachment.Data)
		digest := hex.EncodeToString(sum[:])
		name := digest + extensionFor(attachment)
		relative := filepath.Join(folder, day, name)
		full := filepath.Join(dir, name)

		if _, err := os.Stat(full); err != nil {
			if err := writeFileAtomically(dir, full, attachment.Data); err != nil {
				common.SysError("chat record: cannot store an attachment: " + err.Error())
				continue
			}
		}
		stored = append(stored, StoredAttachment{
			Attachment: attachment,
			SHA256:     digest,
			Size:       int64(len(attachment.Data)),
			Path:       filepath.ToSlash(relative),
		})
	}
	return stored
}

// writeFileAtomically writes through a temporary name and renames into place.
// Two workers can be storing the same picture at the same moment, and the
// serving endpoint can be reading it while they do; a rename is the only way
// for a reader to see either nothing or the whole file, never half of one.
func writeFileAtomically(dir, full string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return err
	}
	// Whatever happens below, the temporary file must not be left behind.
	defer os.Remove(temp.Name())

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp.Name(), full)
}

// safeIdentifier reduces a value to what is safe as a path segment and as a
// URL segment at once. A staff number is typed by a person and reaches both
// places unescaped, so the whitelist is the same in both — the only thing that
// differs is how long the far end tolerates and what an empty result should
// become. The output is ASCII by construction, so a byte cut is a rune cut.
func safeIdentifier(value string, max int, fallback string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, value)
	if cleaned == "" {
		return fallback
	}
	if len(cleaned) > max {
		cleaned = cleaned[:max]
	}
	return cleaned
}

// sanitizeFolder keeps a staff id from naming anywhere but its own folder.
// Unassigned traffic still needs somewhere to go, hence the fallback.
func sanitizeFolder(staffID string) string {
	return safeIdentifier(staffID, 64, "unassigned")
}

func extensionFor(attachment Attachment) string {
	if attachment.FileName != "" {
		if ext := filepath.Ext(attachment.FileName); ext != "" && len(ext) <= 10 {
			return strings.ToLower(ext)
		}
	}
	if attachment.MediaType != "" {
		if exts, err := mime.ExtensionsByType(attachment.MediaType); err == nil && len(exts) > 0 {
			return exts[0]
		}
	}
	if attachment.Kind == "image" {
		return ".bin"
	}
	return ".bin"
}

// ResolveStoredPath turns a stored relative path back into a file on disk,
// refusing anything that points outside the configured root.
func ResolveStoredPath(relative string) (string, error) {
	root := operation_setting.GetChatRecordSetting().FileRootOrDefault()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// Stored paths are always relative to the root. An absolute one means the
	// row was written by something other than this code, so refuse it rather
	// than quietly reinterpreting it under the root.
	if strings.HasPrefix(relative, "/") || filepath.IsAbs(filepath.FromSlash(relative)) {
		return "", fmt.Errorf("chat record: stored path must be relative")
	}
	candidate := filepath.Join(absRoot, filepath.FromSlash(relative))
	cleaned := filepath.Clean(candidate)
	if cleaned != absRoot && !strings.HasPrefix(cleaned, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("chat record: refusing a path outside the attachment root")
	}
	return cleaned, nil
}

// ExtractReplyAttachments finds what the model produced, as opposed to what
// somebody attached to their question. Both end up in the same table; the
// origin column is what tells them apart, and it is the difference that makes
// "which question was this picture drawn for" answerable at all.
//
// The reply arrives in one of two shapes. A finished document can be walked
// directly. A stream cannot: it is SSE text, not JSON, and the whole document
// only appears inside one of its events — so the events are unwrapped and each
// walked in turn, with repeats folded, since a stream restates the response as
// it completes.
func ExtractReplyAttachments(body []byte, limit int) []Attachment {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if trimmed == "" {
		return nil
	}

	var found []Attachment
	seen := make(map[string]bool, 8)
	add := func(a Attachment) {
		if len(found) >= 32 || len(a.Data) > limit {
			return
		}
		if len(a.Data) == 0 && a.SourceURL == "" {
			return
		}
		// A stream repeats itself; decoding the same picture five times and
		// leaving the database to reject four of them is work nobody needs.
		mark := a.SourceURL
		if mark == "" {
			sum := sha256.Sum256(a.Data)
			mark = string(sum[:])
		}
		if seen[mark] {
			return
		}
		seen[mark] = true
		a.Origin = OriginReply
		found = append(found, a)
	}

	if strings.HasPrefix(trimmed, "{") && gjson.Valid(trimmed) {
		walkReply(gjson.Parse(trimmed), add, 0)
		return found
	}

	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" || !gjson.Valid(payload) {
			continue
		}
		walkReply(gjson.Parse(payload), add, 0)
	}
	return found
}

// walkReply covers the shapes a reply carries a file in. The depth guard is for
// the streamed events, which wrap the very document this function also reads.
func walkReply(doc gjson.Result, add func(Attachment), depth int) {
	if depth > 3 {
		return
	}

	// Responses: the finished list, and the single item a stream announces.
	doc.Get("output").ForEach(func(_, item gjson.Result) bool {
		replyItem(item, add)
		return true
	})
	replyItem(doc.Get("item"), add)
	if response := doc.Get("response"); response.IsObject() {
		walkReply(response, add, depth+1)
	}

	// Chat Completions. Some providers hang generated pictures off the message
	// rather than putting them in its content.
	doc.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		message := choice.Get("message")
		replyContent(message.Get("content"), add)
		message.Get("images").ForEach(func(_, image gjson.Result) bool {
			add(fromURLOrData("image", urlOf(image.Get("image_url")), ""))
			return true
		})
		return true
	})

	// Claude Messages.
	replyContent(doc.Get("content"), add)

	// The images endpoints, whose whole answer is the picture. This gateway's
	// upstream only ever returns base64, but a provider that returns a link is
	// worth noting too — the link is recorded, not fetched.
	doc.Get("data").ForEach(func(_, item gjson.Result) bool {
		if encoded := item.Get("b64_json").String(); encoded != "" {
			add(fromBase64("image", encoded, "image/png", ""))
			return true
		}
		if url := item.Get("url").String(); url != "" {
			add(Attachment{Kind: "image", SourceURL: url})
		}
		return true
	})
}

func replyItem(item gjson.Result, add func(Attachment)) {
	switch item.Get("type").String() {
	case "image_generation_call":
		// The built-in tool hands back bare base64 with no media type; PNG is
		// what it produces, and the extension only decides the stored filename.
		add(fromBase64("image", item.Get("result").String(), "image/png", ""))
	case "message":
		replyContent(item.Get("content"), add)
	}
}

func replyContent(content gjson.Result, add func(Attachment)) {
	if !content.IsArray() {
		return
	}
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "output_image", "image_url":
			add(fromURLOrData("image", firstNonEmpty(
				urlOf(part.Get("image_url")),
				part.Get("result").String(),
			), ""))
		case "image":
			source := part.Get("source")
			switch source.Get("type").String() {
			case "base64":
				add(fromBase64("image", source.Get("data").String(),
					source.Get("media_type").String(), ""))
			case "url":
				add(Attachment{Kind: "image", SourceURL: source.Get("url").String()})
			}
		}
		return true
	})
}

// urlOf reads an image_url written either way: a bare string, or an object with
// the string inside it.
func urlOf(value gjson.Result) string {
	if value.IsObject() {
		return value.Get("url").String()
	}
	return value.String()
}

// FromMultipart reads what a form-encoded request carried: the picture an edit
// was to be made from, and the words describing the edit.
//
// This is the one thing this package reads on the request path. A multipart
// body is not JSON and cannot be walked like one, and the form is only
// readable while the request is still alive — the server discards its temp
// files as soon as the handler returns, so there is nothing left for a worker
// to open. The relay has already read the same files to send them upstream, so
// the pages are warm and this is a copy rather than a fetch; it is still
// bounded by the same per-file limit as everything else, and it only happens
// when attachments are being kept at all.
func FromMultipart(form *multipart.Form, limit int) (prompt string, files []Attachment) {
	if form == nil {
		return "", nil
	}
	if values := form.Value["prompt"]; len(values) > 0 {
		prompt = values[0]
	}

	for _, headers := range form.File {
		for _, header := range headers {
			if len(files) >= 32 {
				return prompt, files
			}
			if header.Size > int64(limit) {
				continue
			}
			opened, err := header.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(opened, int64(limit)+1))
			_ = opened.Close()
			if err != nil || len(data) == 0 || len(data) > limit {
				continue
			}
			files = append(files, Attachment{
				Kind:      kindForUpload(header.Filename, data),
				Origin:    OriginPrompt,
				MediaType: header.Header.Get("Content-Type"),
				FileName:  filepath.Base(header.Filename),
				Data:      data,
			})
		}
	}
	return prompt, files
}

// kindForUpload separates pictures from everything else, which is the only
// distinction the transcript draws.
func kindForUpload(name string, data []byte) string {
	if strings.HasPrefix(http.DetectContentType(data), "image/") {
		return "image"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
		return "image"
	}
	return "file"
}
