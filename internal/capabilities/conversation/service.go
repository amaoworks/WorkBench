package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

type Service struct {
	db      *sql.DB
	queries *dbsqlc.Queries
	gateway *ai.Gateway
	now     func() time.Time
}

type ChatRequest struct {
	ConversationID string       `json:"conversationId"`
	Message        string       `json:"message"`
	Profile        string       `json:"profile"`
	Context        *PageContext `json:"context,omitempty"`
}

type PageContext struct {
	Route string `json:"route"`
	Label string `json:"label"`
}

var ErrInvalidRequest = errors.New("invalid chat request")

type ChatResponse struct {
	ConversationID string            `json:"conversationId"`
	MessageID      string            `json:"messageId"`
	Text           string            `json:"text"`
	Usage          contracts.AIUsage `json:"usage"`
}

type chatTurn struct {
	conversationID string
	messageID      string
	profile        string
	messages       []contracts.AIMessage
}

func NewService(db *sql.DB, gateway *ai.Gateway) (*Service, error) {
	if db == nil || gateway == nil {
		return nil, errors.New("database and AI gateway are required")
	}
	return &Service{db: db, queries: dbsqlc.New(db), gateway: gateway, now: time.Now}, nil
}

func (s *Service) Available() bool { return s.gateway.Available() }

func (s *Service) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if !s.Available() {
		return ChatResponse{}, ai.ErrUnavailable
	}
	turn, err := s.begin(ctx, request)
	if err != nil {
		return ChatResponse{}, err
	}
	result, err := s.gateway.Generate(ctx, contracts.GenerateRequest{
		Profile: turn.profile, Messages: turn.messages, ConversationID: turn.conversationID, MessageID: turn.messageID,
	})
	if err != nil {
		return ChatResponse{}, err
	}
	return s.complete(ctx, turn, result)
}

func (s *Service) begin(ctx context.Context, request ChatRequest) (chatTurn, error) {
	request.Message = strings.TrimSpace(request.Message)
	if request.Message == "" || len(request.Message) > 50_000 {
		return chatTurn{}, fmt.Errorf("%w: message must contain 1 to 50000 bytes", ErrInvalidRequest)
	}
	pageContext, err := validatePageContext(request.Context)
	if err != nil {
		return chatTurn{}, err
	}
	if request.Profile == "" {
		request.Profile = "default"
	}
	conversationID := request.ConversationID
	newConversation := conversationID == ""
	if newConversation {
		var err error
		conversationID, err = identity.New()
		if err != nil {
			return chatTurn{}, err
		}
	}
	messageID, err := identity.New()
	if err != nil {
		return chatTurn{}, err
	}
	now := s.now().UTC().UnixMilli()
	content, _ := json.Marshal(request.Message)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return chatTurn{}, err
	}
	defer tx.Rollback()
	queries := dbsqlc.New(tx)
	if newConversation {
		title := []rune(request.Message)
		if len(title) > 60 {
			title = title[:60]
		}
		err = queries.InsertConversation(ctx, dbsqlc.InsertConversationParams{
			ID: conversationID, Title: string(title), CreatedAt: now, UpdatedAt: now,
		})
	} else {
		rows, updateErr := queries.TouchConversation(ctx, dbsqlc.TouchConversationParams{UpdatedAt: now, ID: conversationID})
		if updateErr != nil {
			return chatTurn{}, updateErr
		}
		if rows != 1 {
			return chatTurn{}, sql.ErrNoRows
		}
	}
	if err != nil {
		return chatTurn{}, err
	}
	err = queries.InsertUserMessage(ctx, dbsqlc.InsertUserMessageParams{
		ID: messageID, ConversationID: conversationID, ContentJson: string(content), CreatedAt: now,
	})
	if err != nil {
		return chatTurn{}, err
	}
	if err := tx.Commit(); err != nil {
		return chatTurn{}, err
	}

	messages, err := s.messages(ctx, conversationID, 50)
	if err != nil {
		return chatTurn{}, err
	}
	if pageContext != nil {
		messages = append([]contracts.AIMessage{contextMessage(*pageContext)}, messages...)
	}
	return chatTurn{conversationID: conversationID, messageID: messageID, profile: request.Profile, messages: messages}, nil
}

func validatePageContext(value *PageContext) (*PageContext, error) {
	if value == nil {
		return nil, nil
	}
	context := &PageContext{Route: strings.TrimSpace(value.Route), Label: strings.TrimSpace(value.Label)}
	if context.Label == "" || len(context.Label) > 200 {
		return nil, fmt.Errorf("%w: context label must contain 1 to 200 bytes", ErrInvalidRequest)
	}
	if context.Route == "" || len(context.Route) > 2_048 {
		return nil, fmt.Errorf("%w: context route must contain 1 to 2048 bytes", ErrInvalidRequest)
	}
	parsed, err := url.Parse(context.Route)
	if err != nil || !strings.HasPrefix(context.Route, "/") || strings.HasPrefix(context.Route, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: context route must be a site-relative path without a fragment", ErrInvalidRequest)
	}
	return context, nil
}

func contextMessage(value PageContext) contracts.AIMessage {
	content, _ := json.Marshal(fmt.Sprintf(
		"The user is currently viewing the Workbench page %q at route %q. Use this only as navigation context. Do not claim to have read page data unless a tool provided it.",
		value.Label,
		value.Route,
	))
	return contracts.AIMessage{Role: "system", Content: content}
}

func (s *Service) stream(ctx context.Context, request ChatRequest) (chatTurn, contracts.AIStream, error) {
	if !s.Available() {
		return chatTurn{}, nil, ai.ErrUnavailable
	}
	turn, err := s.begin(ctx, request)
	if err != nil {
		return chatTurn{}, nil, err
	}
	stream, err := s.gateway.Stream(ctx, contracts.GenerateRequest{
		Profile: turn.profile, Messages: turn.messages, ConversationID: turn.conversationID, MessageID: turn.messageID,
	})
	if err != nil {
		return chatTurn{}, nil, err
	}
	return turn, stream, nil
}

func (s *Service) complete(ctx context.Context, turn chatTurn, result contracts.GenerateResult) (ChatResponse, error) {
	assistantID, err := identity.New()
	if err != nil {
		return ChatResponse{}, err
	}
	assistantContent, _ := json.Marshal(result.Text)
	err = s.queries.InsertAssistantMessage(ctx, dbsqlc.InsertAssistantMessageParams{
		ID: assistantID, ConversationID: turn.conversationID, ContentJson: string(assistantContent),
		Model: sql.NullString{String: turn.profile, Valid: true}, CreatedAt: s.now().UTC().UnixMilli(),
	})
	if err != nil {
		return ChatResponse{}, fmt.Errorf("save assistant message: %w", err)
	}
	return ChatResponse{
		ConversationID: turn.conversationID, MessageID: assistantID, Text: result.Text, Usage: result.Usage,
	}, nil
}

func (s *Service) messages(ctx context.Context, conversationID string, limit int) ([]contracts.AIMessage, error) {
	rows, err := s.queries.ListRecentConversationMessages(ctx, dbsqlc.ListRecentConversationMessagesParams{
		ConversationID: conversationID, Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	messages := make([]contracts.AIMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, contracts.AIMessage{Role: row.Role, Content: json.RawMessage(row.ContentJson)})
	}
	return messages, nil
}
