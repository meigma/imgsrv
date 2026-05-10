package authz

const (
	// ActionContentWrite permits upload, draft, publish, and alias mutation operations.
	ActionContentWrite = "content.write"

	// ActionAuthManage permits future auth-management operations.
	ActionAuthManage = "auth.manage"

	// ResourceContent is the app-wide content authorization resource.
	ResourceContent = "content"
)
