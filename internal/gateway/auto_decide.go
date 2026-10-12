package gateway

import (
	"context"

	"github.com/yetone/magpie/internal/provider"
)

// SystemOneDecision gives owned Auto tasks the same decision transport,
// provider authentication, rate limits and usage ledger as normal routing.
// Questions and validation belong to the caller; this returns normalized
// System One JSON without exposing provider credentials to the GUI.
func (s *Server) SystemOneDecision(ctx context.Context, p provider.Provider, model string, body []byte) ([]byte, error) {
	return s.systemOne(ctx, p, model, body)
}
