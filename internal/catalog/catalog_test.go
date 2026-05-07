package catalog

import (
	"testing"

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
				require.Error(t, err)
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

func TestNameVersionAndAliasValidation(t *testing.T) {
	tests := []struct {
		name     string
		validate func(string) error
		valid    string
		invalid  string
	}{
		{
			name:     "image name",
			validate: ValidateImageName,
			valid:    "debian_12-minimal",
			invalid:  "Debian",
		},
		{
			name:     "version",
			validate: ValidateVersion,
			valid:    "v1.0.0-rc.1",
			invalid:  "-v1",
		},
		{
			name:     "alias",
			validate: ValidateAlias,
			valid:    "latest-stable",
			invalid:  "bad alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.validate(tt.valid))

			err := tt.validate(tt.invalid)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestArtifactValidation(t *testing.T) {
	require.NoError(t, ValidateArtifactFormat(ArtifactFormatRaw))
	require.NoError(t, ValidateArtifactFormat(ArtifactFormatRawGZ))
	require.NoError(t, ValidateArtifactFormat(ArtifactFormatQCOW2))

	err := ValidateArtifactFormat("vmdk")
	require.ErrorIs(t, err, ErrInvalid)

	require.NoError(t, ValidateToken("architecture", "x86_64"))

	err = ValidateToken("architecture", "x86 64")
	require.ErrorIs(t, err, ErrInvalid)

	require.NoError(t, ValidateNonNegativeSize("size", 0))

	err = ValidateNonNegativeSize("size", -1)
	require.ErrorIs(t, err, ErrInvalid)

	require.NoError(t, ValidateRequiredText("media type", "application/octet-stream"))

	err = ValidateRequiredText("media type", " \t\n")
	require.ErrorIs(t, err, ErrInvalid)
}
