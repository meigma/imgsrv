package uploads

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDigest(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "accepts sha256 lowercase hex digest",
			raw:  "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		{
			name:    "rejects missing algorithm",
			raw:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "rejects uppercase hex",
			raw:     "sha256:0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
			wantErr: true,
		},
		{
			name:    "rejects short digest",
			raw:     "sha256:0123456789abcdef",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDigest(tt.raw)

			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalid)
				assert.Empty(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, Digest(tt.raw), got)
			assert.Equal(t, tt.raw, got.String())
		})
	}
}

func TestPartAndSizeValidation(t *testing.T) {
	require.NoError(t, ValidatePartNumber(1))
	require.NoError(t, ValidatePartNumber(maxPartNumber))
	require.ErrorIs(t, ValidatePartNumber(0), ErrInvalid)
	require.ErrorIs(t, ValidatePartNumber(maxPartNumber+1), ErrInvalid)

	require.NoError(t, ValidateNonNegativeSize("size", 0))
	require.NoError(t, ValidateNonNegativeSize("size", 1))
	require.ErrorIs(t, ValidateNonNegativeSize("size", -1), ErrInvalid)
}

func TestStagingKey(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	got := StagingKey(id)

	assert.Equal(t, "staging/uploads/11111111-2222-3333-4444-555555555555", got)
}

func TestTextValidation(t *testing.T) {
	value := "artifact.img"

	require.NoError(t, ValidateRequiredText("etag", "abc123"))
	require.NoError(t, ValidateOptionalText("filename hint", nil))
	require.NoError(t, ValidateOptionalText("filename hint", &value))

	require.ErrorIs(t, ValidateRequiredText("etag", " \t\n"), ErrInvalid)

	emptyValue := " "
	require.ErrorIs(t, ValidateOptionalText("filename hint", &emptyValue), ErrInvalid)
}
