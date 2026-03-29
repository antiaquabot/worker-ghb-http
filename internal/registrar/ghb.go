package registrar

import (
	"context"

	"github.com/stroi-homes/worker-ghb-http/internal/config"
)

// Registrar performs auto-registration on the developer's website.
type Registrar interface {
	Register(ctx context.Context, objectID string, personalData config.PersonalData) error
}

// GHBRegistrar implements Registrar for GHB via direct HTTP requests.
// TODO: Implement actual registration logic by analyzing GHB website:
//   - Session initiation
//   - CSRF token extraction
//   - Form submission
type GHBRegistrar struct {
	// httpClient, session state, etc. will be added here
}

func NewGHBRegistrar() *GHBRegistrar {
	return &GHBRegistrar{}
}

// Register performs the GHB online registration flow.
func (r *GHBRegistrar) Register(ctx context.Context, objectID string, personalData config.PersonalData) error {
	// TODO: Implement GHB registration via HTTP:
	// 1. GET registration page → extract CSRF token / session
	// 2. POST registration form with personalData fields
	// 3. Handle confirmation / error response
	// 4. Return nil on success, error on failure
	panic("GHBRegistrar.Register: not yet implemented — see TODO in registrar/ghb.go")
}
