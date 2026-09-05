package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type responsesStreamFailure struct {
	EventType string
	Type      string
	Code      string
	Message   string
	RequestID string
	Retryable bool
	cause     error
}

func (e *responsesStreamFailure) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Responses API 请求失败"
	}
	detail := message
	if e.EventType != "" {
		detail += " (event=" + e.EventType + ")"
	}
	if cause := responsesFailureCause(e.cause); cause != "" {
		detail += " (cause=" + cause + ")"
	}
	if e.Code != "" {
		detail += " (code=" + e.Code + ")"
	}
	if e.RequestID != "" {
		detail += " (request_id=" + e.RequestID + ")"
	}
	return "模型接口返回错误: " + detail
}

func (e *responsesStreamFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func isRetryableResponsesStreamError(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var failure *responsesStreamFailure
	if errors.As(err, &failure) {
		return failure.Retryable
	}
	return isRetryableRequestError(ctx, err)
}

func responsesFailureFromEvent(event responsesStreamEvent, header http.Header) *responsesStreamFailure {
	providerErr := event.Error
	if providerErr == nil && event.Response != nil {
		providerErr = event.Response.Error
	}
	failure := &responsesStreamFailure{
		EventType: event.Type,
		RequestID: providerRequestID(header),
		Retryable: event.Type == "response.failed" || event.Type == "response.incomplete",
	}
	if providerErr != nil {
		failure.Type = strings.TrimSpace(providerErr.Type)
		failure.Code = strings.TrimSpace(providerErr.Code)
		failure.Message = strings.TrimSpace(providerErr.Message)
	}
	// Request/auth/schema failures are deterministic and must not be replayed.
	nonRetryable := strings.ToLower(failure.Type + " " + failure.Code)
	for _, marker := range []string{"invalid", "authentication", "authorization", "permission", "not_found", "unsupported", "context_length", "rate_limit"} {
		if strings.Contains(nonRetryable, marker) {
			failure.Retryable = false
			break
		}
	}
	return failure
}

func responsesReadFailure(err error, header http.Header) *responsesStreamFailure {
	kind, retryable := "stream_read", true
	if errors.Is(err, bufio.ErrTooLong) {
		kind, retryable = "frame_limit", false
	}
	return &responsesStreamFailure{EventType: kind, Message: "读取 Responses 流式响应失败", RequestID: providerRequestID(header), Retryable: retryable, cause: err}
}

func responsesFailureCause(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected EOF"
	case errors.Is(err, io.EOF):
		return "EOF"
	case errors.Is(err, bufio.ErrTooLong):
		return "SSE frame exceeds size limit"
	case errors.Is(err, context.Canceled):
		return "context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network timeout"
		}
		return "network read error"
	}
	return fmt.Sprintf("%T", err)
}
