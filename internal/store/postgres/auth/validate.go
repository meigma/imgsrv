package auth

import domain "github.com/meigma/imgsrv/internal/auth"

// validateCreateTokenParams validates the inputs for CreateToken.
func validateCreateTokenParams(params domain.CreateTokenParams) error {
	if err := domain.ValidateTokenID(params.ID); err != nil {
		return err
	}
	if err := domain.ValidateRequiredText("token name", params.Name); err != nil {
		return err
	}
	if err := domain.ValidateTokenPrefix(params.TokenPrefix); err != nil {
		return err
	}

	return domain.ValidateTokenHash(params.TokenHash)
}

// validateLookupActiveTokenParams validates the inputs for LookupActiveToken.
func validateLookupActiveTokenParams(params domain.LookupActiveTokenParams) error {
	if err := domain.ValidateTokenPrefix(params.TokenPrefix); err != nil {
		return err
	}

	return domain.ValidateTokenHash(params.TokenHash)
}

// validateMarkTokenUsedParams validates the inputs for MarkTokenUsed.
func validateMarkTokenUsedParams(params domain.MarkTokenUsedParams) error {
	return domain.ValidateTokenID(params.ID)
}

// validateRevokeTokenParams validates the inputs for RevokeToken.
func validateRevokeTokenParams(params domain.RevokeTokenParams) error {
	return domain.ValidateTokenID(params.ID)
}
