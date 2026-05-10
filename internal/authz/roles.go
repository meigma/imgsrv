package authz

import (
	"context"
	"fmt"
	"strings"

	"github.com/meigma/authkit"
)

// RoleStore is the authkit role-management subset imgsrv uses for built-in roles.
type RoleStore interface {
	authkit.RoleCreator
	authkit.RoleActionGranter
}

// EnsureBuiltinRoles creates imgsrv's built-in auth roles when they are missing.
func EnsureBuiltinRoles(ctx context.Context, store RoleStore) error {
	if store == nil {
		return nil
	}

	if err := ensureRole(ctx, store, authkit.CreateRoleRequest{
		ID:          RoleContentWriter,
		DisplayName: "Content writer",
		Description: "Can write imgsrv content.",
	}, ActionContentWrite); err != nil {
		return err
	}

	return ensureRole(ctx, store, authkit.CreateRoleRequest{
		ID:          RoleAuthManager,
		DisplayName: "Auth manager",
		Description: "Can manage imgsrv authentication policy.",
	}, ActionAuthManage)
}

func ensureRole(ctx context.Context, store RoleStore, role authkit.CreateRoleRequest, action string) error {
	if _, err := store.CreateRole(ctx, role); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("authz: create built-in role %q: %w", role.ID, err)
	}
	if err := store.GrantRoleAction(ctx, authkit.GrantRoleActionRequest{
		RoleID: role.ID,
		Action: action,
	}); err != nil {
		return fmt.Errorf("authz: grant built-in role %q action %q: %w", role.ID, action, err)
	}

	return nil
}

func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
