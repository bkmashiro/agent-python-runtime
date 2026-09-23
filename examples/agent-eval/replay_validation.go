package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// Check the lossless transport record against its parsed continuation before
// trusting the latter. No network operation is reachable here.
func validateProviderExchange(call privateProviderExchange, model string) error {
	if !call.Completed {
		return errors.New("provider exchange is incomplete")
	}
	if call.ResponseTruncated {
		return errors.New("provider response was truncated")
	}
	if call.Transport == "test" {
		return nil
	}
	if call.Transport != "http" {
		return errors.New("unknown provider transport")
	}
	expected, err := encodeProviderRequestBody(model, call.Messages, call.Tools, call.MaxTokens)
	if err != nil || !bytes.Equal(expected, call.RequestBody) {
		return errors.New("provider request bytes differ from parsed request")
	}
	var envelope struct {
		Model   string          `json:"model"`
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	decodeErr := json.Unmarshal(call.ResponseBody, &envelope)
	parsed := ProviderResponse{Model: envelope.Model, Usage: decodeUsage(envelope.Usage)}
	if call.Error == "" {
		if call.StatusCode < 200 || call.StatusCode >= 300 || decodeErr != nil || len(envelope.Choices) != 1 {
			return errors.New("successful provider response is invalid")
		}
		if reason := envelope.Choices[0].FinishReason; reason == "length" || reason == "content_filter" {
			return errors.New("provider completion is incomplete")
		}
		parsed.Message = envelope.Choices[0].Message
	}
	saved := cloneProviderResponse(call.Response)
	saved.RawBody = nil
	if !reflect.DeepEqual(parsed, saved) {
		return errors.New("provider response bytes differ from parsed response")
	}
	if call.Error != "" && !bytes.Equal(call.ErrorBody, call.ResponseBody) {
		return errors.New("provider error body differs")
	}
	return nil
}
