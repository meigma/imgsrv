package authz

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/meigma/authkit/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureBootstrapAdminCreatesOneTimeAuthManagerToken(t *testing.T) {
	store := memory.NewStore()
	require.NoError(t, EnsureBuiltinRoles(context.Background(), store))
	var output bytes.Buffer
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	err := EnsureBootstrapAdmin(context.Background(), BootstrapConfig{
		Store:    store,
		Output:   &output,
		Now:      func() time.Time { return now },
		TokenTTL: 24 * time.Hour,
	})

	require.NoError(t, err)
	text := output.String()
	assert.Contains(t, text, "imgsrv bootstrap auth token")
	assert.Contains(t, text, "expires_at: 2026-05-11T12:00:00Z")
	token := bootstrapTokenFromOutput(t, text)

	apiTokens, err := apikey.NewService(store, apikey.WithClock(func() time.Time { return now }))
	require.NoError(t, err)
	identity, err := apiTokens.VerifyToken(context.Background(), token)
	require.NoError(t, err)
	require.NotNil(t, identity)
	resolved, err := store.ResolveIdentity(context.Background(), *identity)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assignments, err := store.ListPrincipalRoleAssignments(context.Background(), resolved.ID)
	require.NoError(t, err)
	assert.Equal(t, []authkit.PrincipalRoleAssignment{
		{PrincipalID: resolved.ID, RoleID: RoleAuthManager},
	}, assignments)

	output.Reset()
	err = EnsureBootstrapAdmin(context.Background(), BootstrapConfig{
		Store:  store,
		Output: &output,
		Now:    func() time.Time { return now },
	})

	require.NoError(t, err)
	assert.Empty(t, output.String())
}

func bootstrapTokenFromOutput(t *testing.T, output string) string {
	t.Helper()

	for line := range strings.SplitSeq(output, "\n") {
		token, ok := strings.CutPrefix(line, "token: ")
		if ok {
			return token
		}
	}
	t.Fatal("bootstrap output did not include token")

	return ""
}
