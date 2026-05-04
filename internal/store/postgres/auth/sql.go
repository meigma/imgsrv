package auth

//nolint:gosec // Column list contains token_prefix; it is not a hardcoded credential.
const tokenColumns = `id,
	name,
	token_prefix,
	created_at,
	last_used_at,
	revoked_at`
