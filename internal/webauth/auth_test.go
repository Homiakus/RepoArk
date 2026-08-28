package webauth

import (
	"github.com/Homiakus/repoark/internal/config"
	"testing"
)

func TestRoleMapping(t *testing.T) {
	cfg := config.WebAuthConfig{ViewerGroups: []string{"readers"}, OperatorGroups: []string{"ops"}, AdminGroups: []string{"admins"}}
	if got := roleForGroups([]string{"OPS"}, cfg); got != RoleOperator {
		t.Fatalf("got %v", got)
	}
	if got := roleForGroups([]string{"readers", "admins"}, cfg); got != RoleAdmin {
		t.Fatalf("got %v", got)
	}
}

func TestOIDCScopesAlwaysContainOpenIDAndDeduplicate(t *testing.T) {
	got := oidcScopes([]string{"profile", "openid", "groups", "profile", ""})
	want := []string{"openid", "profile", "groups"}
	if len(got) != len(want) {
		t.Fatalf("scopes=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scopes=%v", got)
		}
	}
}
