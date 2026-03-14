package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/sashabaranov/go-openai"
)

type OpenAIProvider struct {
	client      *openai.Client
	interceptor *geminiInterceptor
}

func NewOpenAIProvider(client *openai.Client) *OpenAIProvider {
	return &OpenAIProvider{client: client}
}

// NewGeminiCompatibleProvider creates a provider that handles Gemini-specific extensions
func NewGeminiCompatibleProvider(apiKey string, baseURL string) *OpenAIProvider {
	interceptor := &geminiInterceptor{
		rt:         http.DefaultTransport,
		signatures: make(map[string]string),
	}
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	config.HTTPClient = &http.Client{
		Transport: interceptor,
	}
	client := openai.NewClientWithConfig(config)
	return &OpenAIProvider{
		client:      client,
		interceptor: interceptor,
	}
}

func (p *OpenAIProvider) CreateCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*Response, error) {
	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("no choices returned from LLM")
	}

	choice := resp.Choices[0]

	// Try to get thought signature from interceptor if not directly available
	thoughtSig := ""
	if p.interceptor != nil {
		// For non-streaming, we might need a way to correlate, but usually the interceptor
		// captures it during the RoundTrip.
	}

	return &Response{
		Content:          choice.Message.Content,
		ToolCalls:        choice.Message.ToolCalls,
		ThoughtSignature: thoughtSig,
		Usage:            &resp.Usage,
		FinishReason:     string(choice.FinishReason),
	}, nil
}

func (p *OpenAIProvider) CreateStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan StreamResponse, error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamResponse)

	go func() {
		defer close(out)
		defer stream.Close()

		for {
			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					return
				}
				out <- StreamResponse{Error: err}
				return
			}

			if len(response.Choices) > 0 {
				choice := response.Choices[0]

				// Capture thought signature if possible (though go-openai likely strips it)
				// The interceptor's streaming support would be better here.

				out <- StreamResponse{
					Content:      choice.Delta.Content,
					ToolCalls:    choice.Delta.ToolCalls,
					Usage:        response.Usage,
					FinishReason: string(choice.FinishReason),
				}
			}
		}
	}()

	return out, nil
}

type geminiInterceptor struct {
	rt         http.RoundTripper
	signatures map[string]string // Maps tool_call_id to signature
	activeSigs map[int]string    // Maps tool_call_index to accumulated signature (for streaming)
	activeIDs  map[int]string    // Maps tool_call_index to ID (for streaming)
	mu         sync.Mutex
}

func (g *geminiInterceptor) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. Handle outgoing request: Inject thought signatures
	if req.Body != nil && req.Method == "POST" && strings.Contains(req.URL.Path, "chat/completions") {
		body, err := io.ReadAll(req.Body)
		if err == nil {
			var chatReq map[string]interface{}
			if err := json.Unmarshal(body, &chatReq); err == nil {
				if messages, ok := chatReq["messages"].([]interface{}); ok {
					modified := false
					for _, m := range messages {
						msg, ok := m.(map[string]interface{})
						if !ok {
							continue
						}

						// Injection into Assistant messages (for the tool_calls themselves)
						if role, ok := msg["role"].(string); ok && role == "assistant" {
							if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
								for _, tc := range toolCalls {
									toolCall, ok := tc.(map[string]interface{})
									if !ok {
										continue
									}
									if id, ok := toolCall["id"].(string); ok {
										g.mu.Lock()
										if sig, found := g.signatures[id]; found {
											toolCall["thought_signature"] = sig
											modified = true
										}
										g.mu.Unlock()
									}
								}
							}
						}

						// Injection into Tool messages
						if role, ok := msg["role"].(string); ok && role == "tool" {
							if toolID, ok := msg["tool_call_id"].(string); ok {
								g.mu.Lock()
								if sig, found := g.signatures[toolID]; found {
									msg["thought_signature"] = sig
									modified = true
								}
								g.mu.Unlock()
							}
						}
					}
					if modified {
						newBody, _ := json.Marshal(chatReq)
						req.Body = io.NopCloser(bytes.NewReader(newBody))
						req.ContentLength = int64(len(newBody))
					} else {
						req.Body = io.NopCloser(bytes.NewReader(body))
					}
				} else {
					req.Body = io.NopCloser(bytes.NewReader(body))
				}
			} else {
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
	}

	// 2. Execute request
	resp, err := g.rt.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// 3. Handle response: Fix Gemini error array and capture thought signatures
	if resp.StatusCode >= 400 {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			// Check if it's a Gemini error array: [{ "error": ... }]
			var errArray []map[string]interface{}
			if err := json.Unmarshal(body, &errArray); err == nil && len(errArray) > 0 {
				if errObj, ok := errArray[0]["error"]; ok {
					newBody, _ := json.Marshal(map[string]interface{}{"error": errObj})
					resp.Body = io.NopCloser(bytes.NewReader(newBody))
					resp.ContentLength = int64(len(newBody))
				} else {
					resp.Body = io.NopCloser(bytes.NewReader(body))
				}
			} else {
				resp.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
	} else if resp.StatusCode == 200 {
		// IMPORTANT: Resetting active maps for every request might be wrong if multiple calls are needed
		// for one turn, but here each turn is a new request.
		g.mu.Lock()
		g.activeIDs = make(map[int]string)
		g.activeSigs = make(map[int]string)
		g.mu.Unlock()

		isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
		if isStream {
			resp.Body = &streamInterceptor{
				rc:          resp.Body,
				interceptor: g,
			}
		} else {
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				g.captureSignatures(body)
				resp.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
	}

	return resp, nil
}

func (g *geminiInterceptor) captureSignatures(body []byte) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err == nil {
		if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if message, ok := choice["message"].(map[string]interface{}); ok {
					g.mu.Lock()

					// Check for top-level thought_signature in message
					msgSig, _ := message["thought_signature"].(string)

					if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
						singleToolCall := len(toolCalls) == 1
						for _, tc := range toolCalls {
							toolCall, ok := tc.(map[string]interface{})
							if !ok {
								continue
							}
							id, _ := toolCall["id"].(string)
							sig, _ := toolCall["thought_signature"].(string)

							if sig == "" && msgSig != "" && singleToolCall {
								sig = msgSig
							}

							if id != "" && sig != "" {
								g.signatures[id] = sig
							}
						}
					}
					g.mu.Unlock()
				}
			}
		}
	}
}

type streamInterceptor struct {
	rc          io.ReadCloser
	interceptor *geminiInterceptor
	buffer      bytes.Buffer
}

func (g *geminiInterceptor) storeActiveSignatureLocked(idx int) {
	id, hasID := g.activeIDs[idx]
	sig, hasSig := g.activeSigs[idx]
	if hasID && hasSig && sig != "" {
		g.signatures[id] = sig
	}
}

func (g *geminiInterceptor) singleActiveIndexLocked() (int, bool) {
	if len(g.activeIDs) != 1 {
		return 0, false
	}
	for idx := range g.activeIDs {
		return idx, true
	}
	return 0, false
}

func (s *streamInterceptor) Read(p []byte) (n int, err error) {
	n, err = s.rc.Read(p)
	if n > 0 {
		s.buffer.Write(p[:n])
		s.processBuffer()
	}
	return n, err
}

func (s *streamInterceptor) processBuffer() {
	for {
		line, err := s.buffer.ReadString('\n')
		if err != nil {
			s.buffer.WriteString(line)
			return
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err == nil {
				if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
					if choice, ok := choices[0].(map[string]interface{}); ok {
						if delta, ok := choice["delta"].(map[string]interface{}); ok {
							s.interceptor.mu.Lock()

							topLevelSig, _ := delta["thought_signature"].(string)

							if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
								singleToolCall := len(toolCalls) == 1
								for _, tc := range toolCalls {
									toolCall, ok := tc.(map[string]interface{})
									if !ok {
										continue
									}

									var idx int
									if idxVal, ok := toolCall["index"].(float64); ok {
										idx = int(idxVal)
									} else {
										continue
									}

									if id, ok := toolCall["id"].(string); ok && id != "" {
										s.interceptor.activeIDs[idx] = id
									}
									if singleToolCall && topLevelSig != "" {
										s.interceptor.activeSigs[idx] += topLevelSig
									}
									if sig, ok := toolCall["thought_signature"].(string); ok && sig != "" {
										s.interceptor.activeSigs[idx] += sig
									}

									s.interceptor.storeActiveSignatureLocked(idx)
								}
							} else if topLevelSig != "" {
								if idx, ok := s.interceptor.singleActiveIndexLocked(); ok {
									s.interceptor.activeSigs[idx] += topLevelSig
									s.interceptor.storeActiveSignatureLocked(idx)
								}
							}
							s.interceptor.mu.Unlock()
						}
					}
				}
			}
		}
	}
}

func (s *streamInterceptor) Close() error {
	return s.rc.Close()
}
