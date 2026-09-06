package contracts

import (
	"context"
	"errors"
)

var ErrAIUnavailable = errors.New("AI service unavailable")

type TextRequest struct {
	Profile     string
	Instruction string
	Input       string
}

// TextGenerator is the business-facing AI capability. It cannot execute business tools.
type TextGenerator interface {
	GenerateText(context.Context, TextRequest) (string, error)
}
