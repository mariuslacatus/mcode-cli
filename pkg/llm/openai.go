package llm

import (
	"context"
	"errors"
	"io"

	"github.com/sashabaranov/go-openai"
)

type OpenAIProvider struct {
	client *openai.Client
}

func NewOpenAIProvider(client *openai.Client) *OpenAIProvider {
	return &OpenAIProvider{client: client}
}

func (p *OpenAIProvider) CreateCompletion(ctx context.Context, req Request) (*Response, error) {
	resp, err := p.client.CreateChatCompletion(ctx, convertToOpenAIRequest(req))
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("no choices returned from LLM")
	}

	choice := resp.Choices[0]
	return &Response{
		Content:      choice.Message.Content,
		ToolCalls:    choice.Message.ToolCalls,
		Usage:        &resp.Usage,
		FinishReason: string(choice.FinishReason),
	}, nil
}

func (p *OpenAIProvider) CreateStream(ctx context.Context, req Request) (<-chan StreamResponse, error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, convertToOpenAIRequest(req))
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

func convertToOpenAIRequest(req Request) openai.ChatCompletionRequest {
	var messages []openai.ChatCompletionMessage
	for _, m := range req.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		})
	}

	return openai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       req.Tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
}
