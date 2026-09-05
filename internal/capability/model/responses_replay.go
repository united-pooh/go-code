package model

import (
	"context"
	"errors"
	"strings"
)

type responsesEventSink func(StreamEvent) error

// Only published text is replay state. Tool calls stay buffered until completion.
type responsesReplay struct {
	text     strings.Builder
	thinking strings.Builder
}

func (r *responsesReplay) published() bool {
	return r.text.Len() != 0 || r.thinking.Len() != 0
}

func (r *responsesReplay) attempt(ctx context.Context, events chan<- StreamEvent) responsesEventSink {
	textPrefix, thinkingPrefix := r.text.String(), r.thinking.String()
	textOffset, thinkingOffset := 0, 0
	return func(event StreamEvent) error {
		text, nextText, err := matchResponsesReplay(textPrefix, textOffset, event.Delta)
		if err != nil {
			return err
		}
		thinking, nextThinking, err := matchResponsesReplay(thinkingPrefix, thinkingOffset, event.Thinking)
		if err != nil {
			return err
		}
		if event.Done && (nextText != len(textPrefix) || nextThinking != len(thinkingPrefix)) {
			return responsesReplayMismatch()
		}
		event.Delta, event.Thinking = text, thinking
		if text != "" || thinking != "" || event.Done {
			if !emitStreamEvent(ctx, events, event) {
				return ctx.Err()
			}
			r.text.WriteString(text)
			r.thinking.WriteString(thinking)
		}
		textOffset, thinkingOffset = nextText, nextThinking
		return nil
	}
}

func matchResponsesReplay(prefix string, offset int, delta string) (string, int, error) {
	count := min(len(prefix)-offset, len(delta))
	if prefix[offset:offset+count] != delta[:count] {
		return "", offset, responsesReplayMismatch()
	}
	return delta[count:], offset + count, nil
}

func responsesReplayMismatch() error {
	return &responsesStreamFailure{EventType: "replay_mismatch", Message: "Responses 重放与已显示内容不一致，已停止恢复以避免重复或混合输出"}
}

func canRetryResponsesStream(ctx context.Context, err error, published bool) bool {
	if !isRetryableResponsesStreamError(ctx, err) {
		return false
	}
	if !published {
		return true
	}
	var failure *responsesStreamFailure
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.EventType {
	case "stream_read", "stream_idle", "unexpected_eof":
		return true
	default:
		return false
	}
}
