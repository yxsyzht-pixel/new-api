package codex

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The Codex backend only answers Responses calls that stream. A client asking for
// a single JSON document therefore cannot reach it at all — the request comes back
// 400 "Stream must be set to true", which is what one caller here collected 217
// times in a day. The stream carries everything the non-streaming shape needs, so
// the gateway can ask for it and hand back the document the caller wanted.

const (
	codexEventOutputItemDone = "response.output_item.done"
	codexEventCompleted      = "response.completed"
	codexEventFailed         = "response.failed"
	codexEventIncomplete     = "response.incomplete"
)

// handleResponsesAsNonStream consumes the event stream and answers with the single
// response document it describes.
//
// The terminal event already carries the whole envelope — id, model, status, usage,
// every request echo — but with an empty output array, because the items were
// delivered as they finished. Reassembling means putting those items back rather
// than rebuilding text from deltas, so nothing is lost or reinterpreted on the way.
func handleResponsesAsNonStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewError(errors.New("codex channel: empty response"), types.ErrorCodeBadResponse)
	}
	defer service.CloseResponseBodyGracefully(resp)

	var (
		envelope string
		items    []string
	)

	// Consumed directly rather than through the streaming helper: its keep-alive
	// lines would land in the middle of a document that must parse as one JSON value.
	scanner := helper.NewStreamScanner(resp.Body)
	for scanner.Scan() {
		data := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(data, "data:") {
			continue
		}
		data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		eventType := gjson.Get(data, "type").String()
		if failure := openai.ResponsesStreamFailure(eventType, data); failure != nil {
			return nil, failure
		}
		switch eventType {
		case codexEventOutputItemDone:
			if item := gjson.Get(data, "item"); item.Exists() {
				items = append(items, item.Raw)
			}
		case codexEventCompleted, codexEventIncomplete, codexEventFailed:
			envelope = gjson.Get(data, "response").Raw
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, types.NewError(fmt.Errorf("codex channel: reading response stream failed: %w", err), types.ErrorCodeBadResponse)
	}
	if envelope == "" {
		return nil, types.NewErrorWithStatusCode(
			errors.New("codex channel: stream ended without a terminal response"),
			types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	body, err := sjson.SetRawBytes([]byte(envelope), "output", []byte("["+strings.Join(items, ",")+"]"))
	if err != nil {
		return nil, types.NewError(fmt.Errorf("codex channel: assembling response failed: %w", err), types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, types.NewError(fmt.Errorf("codex channel: writing response failed: %w", err), types.ErrorCodeBadResponse)
	}

	return codexResponsesUsage(info, body), nil
}

// addImageToolUsage folds in what drawing the picture cost.
//
// The backend reports a turn's text under response.usage and everything the
// image tool spent under response.tool_usage.image_gen — a separate block with
// its own input and output counts. Reading only the first bills a caller for the
// prose around the picture and nothing for the picture: 186 output tokens went
// uncounted on a plain 1024x1024, and the gap widens with size and quality.
//
// Input is split so each half lands on the rate it is priced at: image tokens
// are charged through ImageRatio, text through the model's own. Output carries
// one rate whatever it holds, so it needs no split.
func addImageToolUsage(usage *dto.Usage, envelope string) {
	node := gjson.Get(envelope, "tool_usage.image_gen")
	if !node.Exists() {
		return
	}

	imageIn := int(node.Get("input_tokens_details.image_tokens").Int())
	textIn := int(node.Get("input_tokens_details.text_tokens").Int())
	if imageIn == 0 && textIn == 0 {
		// Older shapes report only the total, which is charged as text rather
		// than silently dropped.
		textIn = int(node.Get("input_tokens").Int())
	}
	out := int(node.Get("output_tokens").Int())

	usage.PromptTokens += imageIn + textIn
	usage.PromptTokensDetails.ImageTokens += imageIn
	usage.CompletionTokens += out
	usage.TotalTokens += imageIn + textIn + out
}

// codexResponsesUsage reads what the turn cost out of the assembled document and
// counts the billable tool calls it performed, matching what the non-streaming
// handler records for an ordinary OpenAI response.
func codexResponsesUsage(info *relaycommon.RelayInfo, body []byte) *dto.Usage {
	usage := &dto.Usage{}
	if node := gjson.GetBytes(body, "usage"); node.Exists() {
		usage.PromptTokens = int(node.Get("input_tokens").Int())
		usage.CompletionTokens = int(node.Get("output_tokens").Int())
		usage.TotalTokens = int(node.Get("total_tokens").Int())
		usage.PromptTokensDetails.CachedTokens = int(node.Get("input_tokens_details.cached_tokens").Int())
		usage.PromptTokensDetails.CacheWriteTokens = int(node.Get("input_tokens_details.cache_write_tokens").Int())
	}

	addImageToolUsage(usage, string(body))

	// The helper reads the raw JSON value, not the decoded string.
	billable := !relaycommon.IsNonBillableResponsesStatus([]byte(gjson.GetBytes(body, "status").Raw))
	imageCounter := &relaycommon.ImageGenerationCallCounter{}

	gjson.GetBytes(body, "output").ForEach(func(index, item gjson.Result) bool {
		switch item.Get("type").String() {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, item.Get("name").String())
		}
		if billable {
			var output dto.ResponsesOutput
			if err := common.Unmarshal([]byte(item.Raw), &output); err == nil {
				position := int(index.Int())
				imageCounter.Observe(&output, &position)
			}
		}
		return true
	})
	imageCounter.Commit(info)

	return usage
}
