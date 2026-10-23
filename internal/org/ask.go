package org

import (
	"context"
	"net/http"
)

// askMovedMessage explains where a request now goes. Until 0.4, ask stays a
// capability so that a coding agent that still calls it learns why.
const askMovedMessage = "The ask capability was retired. Send the request to the organisation as a message (a text task to its node); the organisation's agent answers it."

// ask answers ask_moved whatever the arguments.
func (s *Service) ask(_ context.Context, _ *call) (any, error) {
	return nil, newErr(http.StatusGone, CodeAskMoved, "%s", askMovedMessage)
}
