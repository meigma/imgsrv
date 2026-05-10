package authz

const (
	// RoleContentWriter is the built-in role assigned by OIDC provisioning rules for publisher writes.
	RoleContentWriter = "content-writer"

	// RoleAuthManager is the built-in role for API-token bootstrap administrators.
	RoleAuthManager = "auth-manager"

	// ActionContentWrite permits upload, draft, publish, and alias mutation operations.
	ActionContentWrite = "content.write"

	// ActionAuthManage permits future auth-management operations.
	ActionAuthManage = "auth.manage"

	// ResourceContent is the app-wide content authorization resource.
	ResourceContent = "content"

	// ResourceAuth is the app-wide auth-management authorization resource.
	ResourceAuth = "auth"
)
