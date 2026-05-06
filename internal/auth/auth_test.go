package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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

func TestParseTokenPrefix(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		want      string
		assertErr func(*testing.T, error)
	}{
		{
			name:  "extracts valid prefix",
			token: "testtok.secret",
			want:  "testtok",
		},
		{
			name:  "trims surrounding whitespace",
			token: " \ttesttok.secret\n",
			want:  "testtok",
		},
		{
			name:  "allows dots in secret",
			token: "testtok.secret.with.dots",
			want:  "testtok",
		},
		{
			name:  "rejects missing separator",
			token: "testtok",
			assertErr: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrInvalid)
			},
		},
		{
			name:  "rejects blank secret",
			token: "testtok.",
			assertErr: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrInvalid)
			},
		},
		{
			name:  "rejects invalid prefix",
			token: "short.secret",
			assertErr: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrInvalid)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTokenPrefix(tt.token)

			if tt.assertErr != nil {
				tt.assertErr(t, err)
				assert.Empty(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHashToken(t *testing.T) {
	got, err := HashToken("testtok.secret")

	require.NoError(t, err)
	assert.Equal(t, "sha256:05a0186a1b7b54828e420d982da73204a37bc2d6994c4a853f7c0852420a2a88", got)
	assert.Len(t, strings.TrimPrefix(got, "sha256:"), 64)

	_, err = HashToken(" \t\n")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestValidateTokenHash(t *testing.T) {
	require.NoError(
		t,
		ValidateTokenHash(
			"sha256:05a0186a1b7b54828e420d982da73204a37bc2d6994c4a853f7c0852420a2a88",
		),
	)
	require.ErrorIs(
		t,
		ValidateTokenHash("05a0186a1b7b54828e420d982da73204a37bc2d6994c4a853f7c0852420a2a88"),
		ErrInvalid,
	)
	require.ErrorIs(
		t,
		ValidateTokenHash(
			"sha256:05A0186A1B7B54828E420D982DA73204A37BC2D6994C4A853F7C0852420A2A88",
		),
		ErrInvalid,
	)
	require.ErrorIs(t, ValidateTokenHash("sha256:short"), ErrInvalid)
}
