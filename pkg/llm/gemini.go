package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

type GeminiProvider struct {
	client *genai.Client
}

func NewGeminiProvider(ctx context.Context, apiKey string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &GeminiProvider{client: client}, nil
}

func buildGenAIConfig(req Request) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{
		Temperature:     genai.Ptr(float32(req.Temperature)),
		MaxOutputTokens: int32(req.MaxTokens),
		TopP:            genai.Ptr(float32(req.TopP)),
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}

	if len(req.Tools) > 0 {
		var functionDecls []*genai.FunctionDeclaration
		for _, t := range req.Tools {
			if t.Type == openai.ToolTypeFunction {
				var params map[string]any
				if t.Function.Parameters != nil {
					data, _ := json.Marshal(t.Function.Parameters)
					json.Unmarshal(data, &params)
				}

				functionDecls = append(functionDecls, &genai.FunctionDeclaration{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  convertToSchema(params),
				})
			}
		}
		if len(functionDecls) > 0 {
			config.Tools = []*genai.Tool{
				{FunctionDeclarations: functionDecls},
			}
		}
	}
	return config
}

func parseCandidateParts(candidate *genai.Candidate) (string, string, []byte, []openai.ToolCall) {
	var content strings.Builder
	var reasoning strings.Builder
	var thoughtSignature []byte
	var toolCalls []openai.ToolCall

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Thought {
				reasoning.WriteString(part.Text)
			} else if part.Text != "" {
				content.WriteString(part.Text)
			}

			if len(part.ThoughtSignature) > 0 {
				thoughtSignature = append(thoughtSignature, part.ThoughtSignature...)
			}

			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   part.FunctionCall.Name,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(args),
					},
				})
			}
		}
	}

	return content.String(), reasoning.String(), thoughtSignature, toolCalls
}

func (p *GeminiProvider) CreateCompletion(ctx context.Context, req Request) (*Response, error) {
	config := buildGenAIConfig(req)

	// Convert messages to Contents
	var contents []*genai.Content
	for _, m := range req.Messages {
		contents = append(contents, convertToContent(m))
	}

	resp, err := p.client.Models.GenerateContent(ctx, req.Model, contents, config)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates returned")
	}

	candidate := resp.Candidates[0]
	content, reasoning, thoughtSignature, toolCalls := parseCandidateParts(candidate)

	return &Response{
		Content:          content,
		Reasoning:        reasoning,
		ThoughtSignature: thoughtSignature,
		ToolCalls:        toolCalls,
		FinishReason:     string(candidate.FinishReason),
	}, nil
}

func (p *GeminiProvider) CreateStream(ctx context.Context, req Request) (<-chan StreamResponse, error) {
	config := buildGenAIConfig(req)

	var contents []*genai.Content
	for _, m := range req.Messages {
		contents = append(contents, convertToContent(m))
	}

	out := make(chan StreamResponse)

	go func() {
		defer close(out)
		iter := p.client.Models.GenerateContentStream(ctx, req.Model, contents, config)
		for resp, err := range iter {
			if err != nil {
				out <- StreamResponse{Error: err}
				return
			}

			if len(resp.Candidates) > 0 {
				candidate := resp.Candidates[0]
				content, reasoning, thoughtSignature, toolCalls := parseCandidateParts(candidate)

				out <- StreamResponse{
					Content:          content,
					Reasoning:        reasoning,
					ThoughtSignature: thoughtSignature,
					ToolCalls:        toolCalls,
					FinishReason:     string(candidate.FinishReason),
				}
			}
		}
	}()

	return out, nil
}

func convertToContent(m Message) *genai.Content {
	role := m.Role
	if role == openai.ChatMessageRoleSystem {
		role = "user"
	} else if role == openai.ChatMessageRoleAssistant {
		role = "model"
	}

	content := &genai.Content{
		Role: role,
	}

	// Important: Order of parts matters for thought signatures.
	// Gemini requires: [Thought with Signature] -> [Text] -> [FunctionCall]

	if m.Reasoning != "" || len(m.ThoughtSignature) > 0 {
		content.Parts = append(content.Parts, &genai.Part{
			Text:             m.Reasoning,
			Thought:          true,
			ThoughtSignature: m.ThoughtSignature,
		})
	}

	if m.Content != "" {
		content.Parts = append(content.Parts, &genai.Part{Text: m.Content})
	}

	for _, tc := range m.ToolCalls {
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		content.Parts = append(content.Parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: tc.Function.Name,
				Args: args,
			},
			ThoughtSignature: m.ThoughtSignature,
		})
	}

	if m.Role == openai.ChatMessageRoleTool {
		var result map[string]any
		json.Unmarshal([]byte(m.Content), &result)
		content.Parts = []*genai.Part{
			{
				FunctionResponse: &genai.FunctionResponse{
					Name:     m.Name,
					Response: result,
				},
			},
		}
	}

	return content
}

func convertToSchema(params map[string]any) *genai.Schema {
	if params == nil {
		return nil
	}

	data, _ := json.Marshal(params)
	var schema genai.Schema
	json.Unmarshal(data, &schema)
	return &schema
}
