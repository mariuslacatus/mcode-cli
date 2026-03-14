package llm

import "testing"

func TestCaptureSignaturesOnlyUsesTopLevelSignatureForSingleToolCall(t *testing.T) {
	t.Run("single tool call inherits top-level signature", func(t *testing.T) {
		interceptor := &geminiInterceptor{
			signatures: make(map[string]string),
		}

		interceptor.captureSignatures([]byte(`{
			"choices": [{
				"message": {
					"thought_signature": "sig-top",
					"tool_calls": [{
						"id": "call-1"
					}]
				}
			}]
		}`))

		if got := interceptor.signatures["call-1"]; got != "sig-top" {
			t.Fatalf("expected top-level signature to be captured for single tool call, got %q", got)
		}
	})

	t.Run("multiple tool calls do not inherit ambiguous top-level signature", func(t *testing.T) {
		interceptor := &geminiInterceptor{
			signatures: make(map[string]string),
		}

		interceptor.captureSignatures([]byte(`{
			"choices": [{
				"message": {
					"thought_signature": "sig-top",
					"tool_calls": [{
						"id": "call-1"
					}, {
						"id": "call-2"
					}]
				}
			}]
		}`))

		if _, ok := interceptor.signatures["call-1"]; ok {
			t.Fatal("unexpected signature captured for first tool call")
		}
		if _, ok := interceptor.signatures["call-2"]; ok {
			t.Fatal("unexpected signature captured for second tool call")
		}
	})
}

func TestStreamInterceptorTopLevelSignatureAttribution(t *testing.T) {
	t.Run("single tool call chunk receives top-level signature", func(t *testing.T) {
		interceptor := &geminiInterceptor{
			signatures: make(map[string]string),
			activeIDs:  make(map[int]string),
			activeSigs: make(map[int]string),
		}
		stream := &streamInterceptor{interceptor: interceptor}

		stream.buffer.WriteString("data: {\"choices\":[{\"delta\":{\"thought_signature\":\"sig-top\",\"tool_calls\":[{\"index\":0,\"id\":\"call-1\"}]}}]}\n")
		stream.processBuffer()

		if got := interceptor.signatures["call-1"]; got != "sig-top" {
			t.Fatalf("expected streamed top-level signature to be captured, got %q", got)
		}
	})

	t.Run("ambiguous top-level signature is ignored", func(t *testing.T) {
		interceptor := &geminiInterceptor{
			signatures: make(map[string]string),
			activeIDs: map[int]string{
				0: "call-1",
				1: "call-2",
			},
			activeSigs: make(map[int]string),
		}
		stream := &streamInterceptor{interceptor: interceptor}

		stream.buffer.WriteString("data: {\"choices\":[{\"delta\":{\"thought_signature\":\"sig-top\"}}]}\n")
		stream.processBuffer()

		if len(interceptor.signatures) != 0 {
			t.Fatalf("expected no signatures to be captured, got %#v", interceptor.signatures)
		}
	})
}
