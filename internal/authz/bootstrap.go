package authz

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	authkitmanagement "github.com/meigma/authkit/management"
)

const (
	defaultBootstrapDisplayName = "bootstrap auth manager"
	defaultBootstrapTokenName   = "bootstrap"
	defaultBootstrapTokenTTL    = 24 * time.Hour
)

// BootstrapStore is the authkit store surface required for first-start bootstrap.
type BootstrapStore interface {
	authkit.PrincipalCreator
	authkit.PrincipalLister
	authkit.PrincipalRoleAssigner
	authkit.PrincipalRoleAssignmentLister
	authkit.IdentityLinker
	apikey.TokenStore
}

// BootstrapConfig configures first-start auth-manager bootstrap.
type BootstrapConfig struct {
	// Store persists the bootstrap principal, role assignment, token, and identity link.
	Store BootstrapStore

	// Output receives the one-time plaintext bootstrap token. Nil discards output.
	Output io.Writer

	// Now returns the current time. Nil selects time.Now.
	Now func() time.Time

	// TokenTTL controls the issued bootstrap token lifetime. Zero selects 24h.
	TokenTTL time.Duration

	// TokenName labels the generated bootstrap token. Empty selects "bootstrap".
	TokenName string

	// DisplayName labels the generated bootstrap principal.
	DisplayName string
}

// EnsureBootstrapAdmin creates a one-time auth-manager principal and API token when none exists.
func EnsureBootstrapAdmin(ctx context.Context, config BootstrapConfig) error {
	if config.Store == nil {
		return nil
	}
	hasAdmin, err := hasAuthManager(ctx, config.Store)
	if err != nil {
		return err
	}
	if hasAdmin {
		return nil
	}

	now := config.Now
	if now == nil {
		now = time.Now
	}
	tokenTTL := config.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = defaultBootstrapTokenTTL
	}
	tokenName := config.TokenName
	if tokenName == "" {
		tokenName = defaultBootstrapTokenName
	}
	displayName := config.DisplayName
	if displayName == "" {
		displayName = defaultBootstrapDisplayName
	}

	apiTokens, err := apikey.NewService(config.Store)
	if err != nil {
		return err
	}
	service := authkitmanagement.NewService(authkitmanagement.Options{
		PrincipalCreator:      config.Store,
		PrincipalRoleAssigner: config.Store,
		IdentityLinker:        config.Store,
		APITokens:             apiTokens,
	})

	principal, err := service.CreatePrincipal(ctx, authkit.CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: displayName,
		Attributes: map[string]any{
			"bootstrap": true,
		},
	})
	if err != nil {
		return err
	}
	issued, err := service.IssueAPIToken(ctx, authkitmanagement.IssueAPITokenRequest{
		PrincipalID: principal.ID,
		Name:        tokenName,
		ExpiresAt:   now().Add(tokenTTL),
	})
	if err != nil {
		return err
	}
	if err := service.AssignPrincipalRole(ctx, authkit.AssignPrincipalRoleRequest{
		PrincipalID: principal.ID,
		RoleID:      RoleAuthManager,
	}); err != nil {
		return err
	}

	output := config.Output
	if output == nil {
		output = io.Discard
	}
	if _, err := fmt.Fprintf(
		output,
		"imgsrv bootstrap auth token\nprincipal_id: %s\ntoken_id: %s\nexpires_at: %s\ntoken: %s\n",
		principal.ID,
		issued.ID,
		issued.ExpiresAt.Format(time.RFC3339),
		issued.Plaintext,
	); err != nil {
		return fmt.Errorf("authz: print bootstrap token: %w", err)
	}

	return nil
}

func hasAuthManager(ctx context.Context, store BootstrapStore) (bool, error) {
	principals, err := store.ListPrincipals(ctx)
	if err != nil {
		return false, err
	}
	for _, principal := range principals {
		assignments, err := store.ListPrincipalRoleAssignments(ctx, principal.ID)
		if err != nil {
			return false, err
		}
		for _, assignment := range assignments {
			if assignment.RoleID == RoleAuthManager {
				return true, nil
			}
		}
	}

	return false, nil
}
