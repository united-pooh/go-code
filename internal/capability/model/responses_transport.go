package model

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"paw/internal/message"
)

type responsesRequestAttempts struct {
	client     *http.Client
	build      func() (*http.Request, error)
	used       int
	limit      int
	retryAfter time.Duration
}

func (a *responsesRequestAttempts) available() bool { return a.used < a.limit }

func (a *responsesRequestAttempts) open(ctx context.Context) (*http.Response, error) {
	for a.available() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if a.used > 0 {
			if err := waitForRequestRetryAfter(ctx, a.used-1, a.retryAfter); err != nil {
				return nil, err
			}
		}
		a.retryAfter = 0
		a.used++
		req, err := a.build()
		if err != nil {
			return nil, err
		}
		resp, err := a.client.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if a.available() && isRetryableRequestError(ctx, err) {
				continue
			}
			return nil, fmt.Errorf("调用模型接口失败: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		failure := providerHTTPErrorFromResponse(resp, "模型接口")
		if !a.available() || !isRetryableHTTPStatus(failure.StatusCode) {
			return nil, failure
		}
		a.retryAfter = failure.RetryAfter
	}
	return nil, fmt.Errorf("Responses request retry budget exhausted")
}

func (c *Client) streamResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, tools, true)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	attempts := responsesRequestAttempts{
		client: c.httpClientForConfig(cfg, true), limit: max(1, cfg.RetryCount+1),
		build: func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
			if err != nil {
				return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
			}
			c.setRequestHeadersForConfig(req, cfg)
			return req, nil
		},
	}
	resp, err := attempts.open(ctx)
	if err != nil {
		return nil, err
	}
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		var replay responsesReplay
		for {
			result := c.consumeResponsesAttempt(ctx, resp, replay.attempt(ctx, events), streamIdleTimeout(cfg), replay.thinking.Len() > 0)
			if result.err == nil {
				return
			}
			if !attempts.available() || !canRetryResponsesStream(ctx, result.err, replay.published()) {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: result.err})
				return
			}
			attempts.retryAfter = result.retryAfter
			var err error
			resp, err = attempts.open(ctx)
			if err != nil {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
				return
			}
		}
	}()
	return events, nil
}
