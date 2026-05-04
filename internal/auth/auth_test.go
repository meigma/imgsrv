package auth

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestValidateTokenPrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		{
			name:   "accepts minimum valid prefix",
			prefix: "abc123",
		},
		{
			name:   "accepts URL-safe token prefix characters",
			prefix: "AbC123_-",
		},
		{
			name:    "rejects short prefix",
			prefix:  "abc12",
			wantErr: true,
		},
		{
			name:    "rejects spaces",
			prefix:  "abc 123",
			wantErr: true,
		},
		{
			name:    "rejects punctuation outside the token alphabet",
			prefix:  "abc123!",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTokenPrefix(tt.prefix)

			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalid)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestValidateTextAndTokenID(t *testing.T) {
	require.NoError(t, ValidateRequiredText("hash", "derived-hash"))
	require.NoError(t, ValidateTokenID(uuid.New()))

	require.ErrorIs(t, ValidateRequiredText("hash", " \t\n"), ErrInvalid)
	require.ErrorIs(t, ValidateTokenID(uuid.Nil), ErrInvalid)
}
