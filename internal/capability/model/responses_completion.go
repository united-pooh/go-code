package model

import "strings"

func reconcileResponsesCompletion(event *StreamEvent, text, thinking string, requireSnapshot bool) error {
	var err error
	event.Delta, err = completedResponsesSuffix(text, event.Delta, requireSnapshot)
	if err != nil {
		return err
	}
	event.Thinking, err = completedResponsesSuffix(thinking, event.Thinking, requireSnapshot)
	return err
}

func completedResponsesSuffix(published, completed string, requireSnapshot bool) (string, error) {
	if completed == "" && !requireSnapshot {
		return "", nil
	}
	if !strings.HasPrefix(completed, published) {
		return "", &responsesStreamFailure{EventType: "completion_mismatch", Message: "Responses 完成快照与已显示内容不一致，未提交工具调用"}
	}
	return completed[len(published):], nil
}
