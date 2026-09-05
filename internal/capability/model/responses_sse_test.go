package model

import (
	"bufio"
	"strings"
	"testing"
)

func TestResponsesSSESupportsBOMAndCarriageReturns(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
			input := "\xef\xbb\xbfdata: {\"type\":" + ending + "data: \"response.completed\"}" + ending + ending
			scanner := bufio.NewScanner(strings.NewReader(input))
			scanner.Split(splitResponsesSSEEvents)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			payload, ok := responsesSSEPayload(scanner.Bytes())
			if !ok || string(payload) != "{\"type\":\n\"response.completed\"}" {
				t.Fatalf("payload=%q, ok=%v", payload, ok)
			}
		})
	}
}

func TestResponsesSSEDoesNotConsumeAmbiguousTrailingCR(t *testing.T) {
	advance, _, err := splitResponsesSSEEvents([]byte("data: x\r\n\r"), false)
	if err != nil || advance != 0 {
		t.Fatalf("advance=%d err=%v before final LF arrives", advance, err)
	}
}
