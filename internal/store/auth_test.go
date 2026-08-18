package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthMembershipRequiresInvitationAfterBootstrapOwner(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	owner, err := st.UpsertOIDCIdentity(t.Context(), "https://issuer.example", "owner", "owner@example.com", "Owner")
	if err != nil || owner.Role != "owner" {
		t.Fatalf("bootstrap owner: identity=%#v err=%v", owner, err)
	}
	if _, err := st.UpsertOIDCIdentity(t.Context(), "https://issuer.example", "guest", "guest@example.com", "Guest"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("uninvited identity: got %v, want ErrForbidden", err)
	}
	if err := st.InviteWorkspaceMember(t.Context(), WorkspaceInvite{
		WorkspaceID: DefaultWorkspaceID, Email: "guest@example.com", Role: "viewer", InvitedBy: owner.User.ID,
	}); err != nil {
		t.Fatal(err)
	}
	guest, err := st.UpsertOIDCIdentity(t.Context(), "https://issuer.example", "guest", "guest@example.com", "Guest")
	if err != nil || guest.Role != "viewer" {
		t.Fatalf("invited identity: identity=%#v err=%v", guest, err)
	}
	if _, err := st.UpsertOIDCIdentity(t.Context(), "https://issuer.example", "guest", "guest@example.com", "Guest Updated"); err != nil {
		t.Fatalf("existing member should not require another invite: %v", err)
	}
	if err := st.UpdateWorkspaceMemberRole(t.Context(), DefaultWorkspaceID, owner.User.ID, "admin"); err == nil {
		t.Fatal("last owner demotion should fail")
	}
}

func TestAuthSessionAndBrokerOAuthFlowsAreSingleUse(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "auth-flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	identity, err := st.UpsertOIDCIdentity(t.Context(), "issuer", "subject", "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CreateAuthSession(t.Context(), AuthSession{
		TokenHash: "token-hash", CSRFTokenHash: "csrf-hash", UserID: identity.User.ID,
		WorkspaceID: identity.Workspace.ID, ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	loaded, session, err := st.GetAuthSession(t.Context(), "token-hash")
	if err != nil || loaded.User.ID != identity.User.ID || session.CSRFTokenHash != "csrf-hash" {
		t.Fatalf("session round trip: identity=%#v session=%#v err=%v", loaded, session, err)
	}
	flow := BrokerOAuthFlow{StateHash: "state", ConnectionID: 42, WorkspaceID: identity.Workspace.ID, UserID: identity.User.ID, ExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano)}
	if err := st.CreateBrokerOAuthFlow(t.Context(), flow); err != nil {
		t.Fatal(err)
	}
	if consumed, err := st.ConsumeBrokerOAuthFlow(t.Context(), "state", now.Format(time.RFC3339Nano)); err != nil || consumed.ConnectionID != 42 {
		t.Fatalf("consume flow: %#v %v", consumed, err)
	}
	if _, err := st.ConsumeBrokerOAuthFlow(t.Context(), "state", now.Format(time.RFC3339Nano)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replayed flow: got %v, want ErrNotFound", err)
	}
}

func TestBootstrapPasswordIdentityIsSingleAndIdempotent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "password-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	credential := PasswordCredential{Username: "owner", PasswordHash: "hash-one", Email: "owner@example.com", Name: "Owner"}
	identity, created, err := st.BootstrapPasswordIdentity(t.Context(), credential)
	if err != nil {
		t.Fatal(err)
	}
	if !created || identity.Role != "owner" || identity.User.Subject != "owner" {
		t.Fatalf("unexpected bootstrap identity: %#v created=%v", identity, created)
	}
	_, created, err = st.BootstrapPasswordIdentity(t.Context(), PasswordCredential{Username: "owner", PasswordHash: "hash-two"})
	if err != nil || created {
		t.Fatalf("idempotent bootstrap: created=%v err=%v", created, err)
	}
	_, passwordHash, err := st.GetPasswordIdentity(t.Context(), " OWNER ")
	if err != nil || passwordHash != "hash-one" {
		t.Fatalf("bootstrap password was unexpectedly changed: hash=%q err=%v", passwordHash, err)
	}
	if _, _, err := st.BootstrapPasswordIdentity(t.Context(), PasswordCredential{Username: "another", PasswordHash: "hash"}); err == nil {
		t.Fatal("second built-in username should be rejected")
	}
}
