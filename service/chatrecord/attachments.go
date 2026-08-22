package chatrecord

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/tidwall/gjson"
)

// Attachments are the images and documents a caller sent along with a message.
// They are pulled out of the request body on a worker, never on the request
// path, and only the path they were written to goes in the database.

type Attachment struct {
	Kind      string // "image" or "file"
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
	if len(body) == 0 || !gjson.ValidBytes(body) {
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

	for _, path := range []string{"messages", "input"} {
		items := gjson.GetBytes(body, path)
		if !items.IsArray() {
			continue
		}
		items.ForEach(func(_, item gjson.Result) bool {
			if content := item.Get("content"); content.IsArray() {
				walkContent(content)
			}
			return true
		})
	}

	// Image edits and variations name the picture at the top level.
	for _, path := range []string{"image", "mask"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type == gjson.String {
			add(fromURLOrData("image", v.String(), ""))
		}
	}

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
			if err := os.MkdirAll(dir, 0o755); err != nil {
				continue
			}
			if err := os.WriteFile(full, attachment.Data, 0o644); err != nil {
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

// sanitizeFolder keeps a staff id from naming anywhere but its own folder.
func sanitizeFolder(staffID string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, staffID)
	if cleaned == "" {
		return "unassigned"
	}
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	return cleaned
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
