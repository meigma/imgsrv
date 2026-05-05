package auth

import domain "github.com/meigma/imgsrv/internal/auth"

// validateLookupActiveTokenParams validates the inputs for LookupActiveToken.
func validateLookupActiveTokenParams(params domain.LookupActiveTokenParams) error {
	if err := domain.ValidateTokenPrefix(params.TokenPrefix); err != nil {
		return err
	}

	return domain.ValidateRequiredText("token hash", params.TokenHash)
}

// validateMarkTokenUsedParams validates the inputs for MarkTokenUsed.
func validateMarkTokenUsedParams(params domain.MarkTokenUsedParams) error {
	return domain.ValidateTokenID(params.ID)
}

// validateRevokeTokenParams validates the inputs for RevokeToken.
func validateRevokeTokenParams(params domain.RevokeTokenParams) error {
	return domain.ValidateTokenID(params.ID)
}
