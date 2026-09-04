package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"workbench/internal/contracts"
)

type Profile struct {
	Model           string
	MaxOutputTokens int64
}

type OpenAIConfig struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Profiles   map[string]Profile
	Timeout    time.Duration
}

type OpenAIProvider struct {
	client   openai.Client
	profiles map[string]Profile
	timeout  time.Duration
}

func NewOpenAIProvider(config OpenAIConfig) (*OpenAIProvider, error) {
	if config.APIKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if len(config.Profiles) == 0 {
		return nil, errors.New("at least one AI profile is required")
	}
	options := []option.RequestOption{option.WithAPIKey(config.APIKey)}
	if config.BaseURL != "" {
		options = append(options, option.WithBaseURL(config.BaseURL))
	}
	if config.HTTPClient != nil {
		options = append(options, option.WithHTTPClient(config.HTTPClient))
	}
	if config.Timeout <= 0 {
		config.Timeout = 2 * time.Minute
	}
	return &OpenAIProvider{client: openai.NewClient(options...), profiles: config.Profiles, timeout: config.Timeout}, nil
}

func (p *OpenAIProvider) Generate(ctx context.Context, request contracts.GenerateRequest) (contracts.GenerateResult, error) {
	params, err := p.params(request)
	if err != nil {
		return contracts.GenerateResult{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	response, err := p.client.Responses.New(requestCtx, params)
	if err != nil {
		return contracts.GenerateResult{}, err
	}
	return resultFromResponse(*response), nil
}

func resultFromResponse(response responses.Response) contracts.GenerateResult {
	result := contracts.GenerateResult{
		ResponseID: response.ID,
		Text:       response.OutputText(),
		Usage: contracts.AIUsage{
			InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens,
		},
	}
	for _, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		result.ToolCalls = append(result.ToolCalls, contracts.ToolCall{
			ID: call.CallID, Name: call.Name, Arguments: json.RawMessage(call.Arguments), IdempotencyKey: call.CallID,
		})
	}
	return result
}

func (p *OpenAIProvider) Stream(ctx context.Context, request contracts.GenerateRequest) (contracts.AIStream, error) {
	params, err := p.params(request)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithTimeout(ctx, p.timeout)
	stream := p.client.Responses.NewStreaming(streamCtx, params)
	result := &openAIStream{events: make(chan contracts.AIStreamEvent, 32), cancel: cancel}
	go func() {
		defer close(result.events)
		defer stream.Close()
		completed := false
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta":
				select {
				case result.events <- contracts.AIStreamEvent{Type: event.Type, Text: event.Delta}:
				case <-streamCtx.Done():
					return
				}
			case "response.completed":
				completed = true
				generated := resultFromResponse(event.Response)
				select {
				case result.events <- contracts.AIStreamEvent{Type: event.Type, Result: &generated, Done: true}:
				case <-streamCtx.Done():
					return
				}
			case "error", "response.failed", "response.incomplete":
				message := event.Message
				if message == "" {
					message = event.Type
				}
				select {
				case result.events <- contracts.AIStreamEvent{Type: event.Type, Err: errors.New(message), Done: true}:
				case <-streamCtx.Done():
				}
				return
			}
		}
		if err := stream.Err(); err != nil {
			select {
			case result.events <- contracts.AIStreamEvent{Type: "error", Err: err, Done: true}:
			case <-streamCtx.Done():
			}
			return
		}
		if !completed {
			select {
			case result.events <- contracts.AIStreamEvent{Type: "error", Err: errors.New("OpenAI stream ended without a completed response"), Done: true}:
			case <-streamCtx.Done():
			}
		}
	}()
	return result, nil
}

func (p *OpenAIProvider) params(request contracts.GenerateRequest) (responses.ResponseNewParams, error) {
	profile, exists := p.profiles[request.Profile]
	if !exists {
		return responses.ResponseNewParams{}, fmt.Errorf("unknown AI profile %q", request.Profile)
	}
	if profile.Model == "" {
		return responses.ResponseNewParams{}, fmt.Errorf("AI profile %q has no model", request.Profile)
	}
	input := make(responses.ResponseInputParam, 0, len(request.Messages)+len(request.ToolOutputs))
	for _, message := range request.Messages {
		text := string(message.Content)
		var decoded string
		if json.Unmarshal(message.Content, &decoded) == nil {
			text = decoded
		}
		role := responses.EasyInputMessageRole(message.Role)
		switch role {
		case responses.EasyInputMessageRoleUser, responses.EasyInputMessageRoleAssistant,
			responses.EasyInputMessageRoleSystem, responses.EasyInputMessageRoleDeveloper:
		default:
			return responses.ResponseNewParams{}, fmt.Errorf("unsupported AI message role %q", message.Role)
		}
		input = append(input, responses.ResponseInputItemParamOfMessage(text, role))
	}
	for _, output := range request.ToolOutputs {
		input = append(input, responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: openai.String(output.CallID),
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfString: openai.String(string(output.Content)),
				},
			},
		})
	}
	tools := make([]responses.ToolUnionParam, 0, len(request.Tools))
	for _, tool := range request.Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.ParametersSchema, &schema); err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("tool %q schema: %w", tool.Name, err)
		}
		tools = append(tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name: tool.Name, Description: openai.String(tool.Description), Parameters: schema, Strict: openai.Bool(true),
		}})
	}
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(profile.Model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Bool(false), Tools: tools,
	}
	if profile.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(profile.MaxOutputTokens)
	}
	if request.PreviousResponseID != "" {
		params.PreviousResponseID = openai.String(request.PreviousResponseID)
	}
	return params, nil
}

type openAIStream struct {
	events chan contracts.AIStreamEvent
	cancel context.CancelFunc
	once   sync.Once
}

func (s *openAIStream) Events() <-chan contracts.AIStreamEvent { return s.events }

func (s *openAIStream) Close() error {
	s.once.Do(s.cancel)
	return nil
}
