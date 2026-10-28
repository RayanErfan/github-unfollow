package git_hub_manager

import (
	"net/http"
	"testing"

	"github.com/google/go-github/v57/github"
)

func TestIsProtected(t *testing.T) {
	t.Setenv(protectedUsersEnv, "")

	tests := []struct {
		username string
		want     bool
	}{
		{"RayanErfan", true},
		{"rayanerfan", true},
		{"  RAYANERFAN  ", true},
		{"someoneelse", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsProtected(tt.username); got != tt.want {
			t.Errorf("IsProtected(%q) = %v, want %v", tt.username, got, tt.want)
		}
	}
}

func TestIsProtectedFromEnv(t *testing.T) {
	t.Setenv(protectedUsersEnv, " alice , bob ")

	tests := []struct {
		username string
		want     bool
	}{
		{"alice", true},
		{"BOB", true},
		{"carol", false},
	}

	for _, tt := range tests {
		if got := IsProtected(tt.username); got != tt.want {
			t.Errorf("IsProtected(%q) = %v, want %v", tt.username, got, tt.want)
		}
	}
}

// TestUnfollowUserNeverCallsTheAPIForProtectedAccounts makes sure the guard
// sits below every code path: no request may be sent for a protected account.
func TestUnfollowUserNeverCallsTheAPIForProtectedAccounts(t *testing.T) {
	t.Setenv(protectedUsersEnv, "")

	calls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})

	gm := newTestManager(t, mux)

	if err := gm.UnfollowUser("rayanerfan", 0); err != nil {
		t.Fatalf("UnfollowUser: %v", err)
	}
	if calls != 0 {
		t.Fatalf("sent %d requests for a protected account, want 0", calls)
	}
}

// TestUnfollowUserSkipsExtraProtectedAccounts covers the accounts coming from
// configuration (.env / PROTECTED_USERS) instead of the hardcoded list.
func TestUnfollowUserSkipsExtraProtectedAccounts(t *testing.T) {
	t.Setenv(protectedUsersEnv, "")

	calls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})

	gm := NewGitHubManagerWithClient(github.NewClient(nil), "me", "Alice")

	if err := gm.UnfollowUser("alice", 0); err != nil {
		t.Fatalf("UnfollowUser: %v", err)
	}
	if calls != 0 {
		t.Fatalf("sent %d requests for a protected account, want 0", calls)
	}
}

func TestExcludeProtected(t *testing.T) {
	t.Setenv(protectedUsersEnv, "")

	gm := NewGitHubManagerWithClient(github.NewClient(nil), "me", "alice")

	got := gm.ExcludeProtected([]string{"alice", "RayanErfan", "bob"})
	want := []string{"bob"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}
