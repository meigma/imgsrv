package auth

import domain "github.com/meigma/imgsrv/internal/auth"

func validateLookupActiveTokenParams(params domain.LookupActiveTokenParams) error {
	if err := domain.ValidateTokenPrefix(params.TokenPrefix); err != nil {
		return err
	}

	return domain.ValidateRequiredText("token hash", params.TokenHash)
}

func validateMarkTokenUsedParams(params domain.MarkTokenUsedParams) error {
	return domain.ValidateTokenID(params.ID)
}

func validateRevokeTokenParams(params domain.RevokeTokenParams) error {
	return domain.ValidateTokenID(params.ID)
}
