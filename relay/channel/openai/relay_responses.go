package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	responsesStreamTypeError         = "error"
	responsesStreamTypeFailed        = "response.failed"
	responsesStreamTypeResponseError = "response.error"
	responsesStreamTypeCreated       = "response.created"
	responsesStreamTypeInProgress    = "response.in_progress"
)

// responsesStreamTypeIsPreamble reports whether an event only announces that the
// upstream accepted the request. Those events carry no model output, so a failure
// arriving while only they have been seen is still recoverable on another channel.
func responsesStreamTypeIsPreamble(eventType string) bool {
	return eventType == responsesStreamTypeCreated || eventType == responsesStreamTypeInProgress
}

// ResponsesStreamFailure turns an in-stream failure event into a relay error.
// Upstream signals capacity and service problems this way — HTTP 200 with an
// `error` event — which would otherwise be billed and logged as a success. It is
// exported because every channel reusing the Responses event stream must classify
// such a failure identically.
func ResponsesStreamFailure(eventType string, data string) *types.NewAPIError {
	switch eventType {
	case responsesStreamTypeError, responsesStreamTypeFailed, responsesStreamTypeResponseError:
	default:
		return nil
	}

	errNode := gjson.Get(data, "error")
	if !errNode.Exists() {
		errNode = gjson.Get(data, "response.error")
	}
	message := strings.TrimSpace(errNode.Get("message").String())
	if message == "" {
		message = "upstream reported a stream failure without a message"
	}
	code := strings.TrimSpace(errNode.Get("code").String())
	rawType := strings.TrimSpace(errNode.Get("type").String())
	if code == "" {
		code = rawType
	}
	if code != "" {
		message = fmt.Sprintf("%s (%s)", message, code)
	}

	if requestFatalResponsesFailureCodes[code] || requestFatalResponsesFailureCodes[rawType] {
		// The request itself is the problem, so no channel can serve it. 400 sits
		// outside the retryable range, which stops the relay from spending two more
		// upstream calls — and two more logged channel errors — on a certain failure.
		return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeBadResponse, http.StatusBadRequest)
	}
	// 503 keeps the failure inside the retryable range so another channel is tried.
	return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeBadResponse, http.StatusServiceUnavailable)
}

// requestFatalResponsesFailureCodes describe the request rather than the channel
// serving it. The list is deliberately narrow: misreading a transient upstream
// problem as fatal would throw away a retry that would have succeeded, so a code
// only belongs here when no other account could possibly accept the same request.
var requestFatalResponsesFailureCodes = map[string]bool{
	"context_length_exceeded": true,
	"invalid_request_error":   true,
	"invalid_prompt":          true,
	"string_above_max_length": true,
	"unsupported_parameter":   true,
	"unsupported_value":       true,
}

// IsRequestFatalResponsesFailure reports whether a failure from
// ResponsesStreamFailure is one that retrying cannot fix.
func IsRequestFatalResponsesFailure(err *types.NewAPIError) bool {
	return err != nil && err.StatusCode == http.StatusBadRequest
}

// shouldCloseTruncatedStream reports whether the client is still waiting for an
// ending that the upstream never sent. A client that has already hung up has
// nobody left to tell.
func shouldCloseTruncatedStream(status *relaycommon.StreamStatus, terminalSent bool) bool {
	if terminalSent || status == nil {
		return false
	}
	return status.EndReason != relaycommon.StreamEndReasonClientGone
}

// sendResponsesTerminalFailure closes a committed stream with an event the
// client recognises as the end of the response.
//
// The Responses protocol ends a stream with response.completed, response.failed
// or response.incomplete. Upstream signals a refusal with a bare `error` event,
// which is none of those — forwarding it alone leaves the client waiting, and it
// eventually gives up with "stream closed before response.completed" instead of
// showing why the request was refused. Emitting response.failed after it carries
// the reason through and lets the client finish cleanly.
func sendResponsesTerminalFailure(c *gin.Context, info *relaycommon.RelayInfo, upstreamType string, upstreamData string, failure *types.NewAPIError) {
	if upstreamType == responsesStreamTypeFailed {
		// Upstream already ended it properly.
		return
	}

	// Report the upstream's own code — a client keying off error.code needs
	// "invalid_prompt", not the relay's internal classification.
	code := string(failure.GetErrorCode())
	if upstream := upstreamFailureCode(upstreamData); upstream != "" {
		code = upstream
	}

	terminal := dto.ResponsesStreamResponse{Type: responsesStreamTypeFailed}
	payload, err := common.Marshal(map[string]any{
		"type": responsesStreamTypeFailed,
		"response": map[string]any{
			"id":     helper.GetResponseID(c),
			"object": "response",
			"model":  info.OriginModelName,
			"status": "failed",
			"error": map[string]any{
				"code":    code,
				"message": failure.Error(),
			},
		},
	})
	if err != nil {
		return
	}
	sendResponsesStreamData(c, terminal, string(payload))
}

// upstreamFailureCode reads the code out of an upstream failure event, matching
// how ResponsesStreamFailure locates it.
func upstreamFailureCode(data string) string {
	errNode := gjson.Get(data, "error")
	if !errNode.Exists() {
		errNode = gjson.Get(data, "response.error")
	}
	if code := strings.TrimSpace(errNode.Get("code").String()); code != "" {
		return code
	}
	return strings.TrimSpace(errNode.Get("type").String())
}

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CacheWriteTokens = responsesResponse.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false
	// An upstream that is out of capacity answers 200 and reports the failure as an
	// in-stream `error` event. Nothing useful has been produced at that point, so the
	// failure is withheld from the client and surfaced as a relay error instead,
	// letting the retry loop reach a channel that can actually serve the request.
	var upstreamStreamErr *types.NewAPIError
	contentStarted := false
	terminalSent := false

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		if !contentStarted && upstreamStreamErr == nil {
			if streamErr := ResponsesStreamFailure(streamResponse.Type, data); streamErr != nil {
				upstreamStreamErr = streamErr
				// Warn, not error: the relay layer still gets to retry this on another
				// channel, and only a failure that survives every attempt is worth
				// putting in front of an operator.
				if IsRequestFatalResponsesFailure(streamErr) {
					logger.LogWarn(c, fmt.Sprintf("upstream rejected the request itself, not retrying: %s", common.LocalLogPreview(streamErr.Error())))
				} else {
					logger.LogWarn(c, fmt.Sprintf("upstream reported failure in stream before any output, will retry: %s", common.LocalLogPreview(streamErr.Error())))
				}
			}
		}
		if upstreamStreamErr != nil {
			if IsRequestFatalResponsesFailure(upstreamStreamErr) {
				// The preamble has already gone to the client, so the response is
				// committed and a relay error can no longer replace it. Withholding
				// here would end the stream with no terminal event at all, which is
				// what a client reports as "stream did not emit a terminal response"
				// — an unhelpful message for a request that was simply refused.
				// Forwarding the upstream failure gives the caller the real reason.
				sendResponsesStreamData(c, streamResponse, data)
				sendResponsesTerminalFailure(c, info, streamResponse.Type, data, upstreamStreamErr)
				sr.Stop(upstreamStreamErr.Err)
				return
			}
			// Withhold the upstream failure events; the relay layer decides what the
			// client finally sees.
			if streamResponse.Type == responsesStreamTypeFailed {
				sr.Stop(upstreamStreamErr.Err)
			}
			return
		}
		if !responsesStreamTypeIsPreamble(streamResponse.Type) {
			contentStarted = true
		}
		sendResponsesStreamData(c, streamResponse, data)
		switch streamResponse.Type {
		case "response.completed", "response.done":
			terminalSent = true
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					if streamResponse.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					}
					if streamResponse.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					}
					if streamResponse.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					}
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
						usage.PromptTokensDetails.CacheWriteTokens = streamResponse.Response.Usage.InputTokensDetails.CacheWriteTokens
					}
				}
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			terminalSent = true
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.output_text.delta":
			// 处理输出文本
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
		}
	})

	// The upstream can die in the middle of a stream — an h2 INTERNAL_ERROR from the
	// peer is the one seen here — and the scanner simply stops reading. Everything
	// already forwarded stays valid, but with no terminal event the client cannot
	// tell the response is over: it waits out its own idle timeout, ten minutes for
	// one caller here, before it retries. Ending the stream ourselves turns that
	// stall into something it can act on at once. A client that has already hung up
	// has nobody left to tell.
	if upstreamStreamErr == nil && shouldCloseTruncatedStream(info.StreamStatus, terminalSent) {
		sendResponsesTerminalFailure(c, info, "", "", types.NewError(
			fmt.Errorf("upstream ended the response stream before it completed (%s)", info.StreamStatus.Summary()),
			types.ErrorCodeBadResponse))
	}

	if upstreamStreamErr != nil {
		return nil, upstreamStreamErr
	}

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}
