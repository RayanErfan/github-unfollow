package git_hub_manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/go-github/v57/github"
)

func newTestManager(t *testing.T, handler http.Handler) *GitHubManager {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	client.BaseURL = base

	return NewGitHubManagerWithClient(client, "me")
}

func writeUsers(t *testing.T, w http.ResponseWriter, logins []string) {
	t.Helper()

	type user struct {
		Login string `json:"login"`
	}

	users := make([]user, 0, len(logins))
	for _, login := range logins {
		users = append(users, user{Login: login})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(users); err != nil {
		t.Fatalf("encode users: %v", err)
	}
}

// TestGetFollowingWalksEveryPage is the regression test for the "only one page"
// bug: the second and third pages must be requested as well.
func TestGetFollowingWalksEveryPage(t *testing.T) {
	pages := [][]string{
		{"alice", "bob"},
		{"carol", "dave"},
		{"erin"},
	}

	var requested []string

	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/following", func(w http.ResponseWriter, r *http.Request) {
		perPage := r.URL.Query().Get("per_page")
		rawPage := r.URL.Query().Get("page")
		requested = append(requested, perPage+"/"+rawPage)

		if perPage != strconv.Itoa(githubMaxPerPage) {
			t.Errorf("per_page = %q, want %d", perPage, githubMaxPerPage)
		}

		page := 1
		if rawPage != "" {
			parsed, err := strconv.Atoi(rawPage)
			if err != nil {
				t.Fatalf("invalid page %q: %v", rawPage, err)
			}
			page = parsed
		}

		if page < len(pages) {
			w.Header().Set("Link", fmt.Sprintf(
				`<http://%s/users/me/following?per_page=%d&page=%d>; rel="next"`,
				r.Host, githubMaxPerPage, page+1,
			))
		}

		writeUsers(t, w, pages[page-1])
	})

	gm := newTestManager(t, mux)

	following, err := gm.GetFollowing()
	if err != nil {
		t.Fatalf("GetFollowing: %v", err)
	}

	want := []string{"alice", "bob", "carol", "dave", "erin"}
	if len(following) != len(want) {
		t.Fatalf("got %d users %v, want %d users %v", len(following), following, len(want), want)
	}
	for i := range want {
		if following[i] != want[i] {
			t.Errorf("user %d = %q, want %q", i, following[i], want[i])
		}
	}

	if len(requested) != len(pages) {
		t.Fatalf("requested %d pages %v, want %d", len(requested), requested, len(pages))
	}
}

// TestGetFollowersDeduplicates makes sure a repeated login across pages does
// not produce duplicate unfollow work.
func TestGetFollowersDeduplicates(t *testing.T) {
	pages := [][]string{
		{"alice", "bob"},
		{"bob", "carol"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/users/someone/followers", func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page == 0 {
			page = 1
		}
		if page < len(pages) {
			w.Header().Set("Link", fmt.Sprintf(
				`<http://%s/users/someone/followers?per_page=%d&page=%d>; rel="next"`,
				r.Host, githubMaxPerPage, page+1,
			))
		}
		writeUsers(t, w, pages[page-1])
	})

	gm := newTestManager(t, mux)

	name := "someone"
	followers, err := gm.GetFollowers(&name)
	if err != nil {
		t.Fatalf("GetFollowers: %v", err)
	}

	want := []string{"alice", "bob", "carol"}
	if len(followers) != len(want) {
		t.Fatalf("got %v, want %v", followers, want)
	}
	for i := range want {
		if followers[i] != want[i] {
			t.Errorf("user %d = %q, want %q", i, followers[i], want[i])
		}
	}
}

// TestUnfollowUserRetriesOnRateLimit verifies a throttled request is retried
// instead of aborting the whole unfollow run.
func TestUnfollowUserRetriesOnRateLimit(t *testing.T) {
	attempts := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/user/following/bob", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	gm := newTestManager(t, mux)

	if err := gm.UnfollowUser("bob", 0); err != nil {
		t.Fatalf("UnfollowUser: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestUnfollowUserGivesUpAfterMaxAttempts keeps a permanently failing user
// from blocking the run forever.
func TestUnfollowUserGivesUpAfterMaxAttempts(t *testing.T) {
	attempts := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/user/following/bob", func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
	})

	gm := newTestManager(t, mux)

	if err := gm.UnfollowUser("bob", 0); err == nil {
		t.Fatal("expected an error after the retries are exhausted")
	}
	if attempts != maxRequestAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, maxRequestAttempts)
	}
}

func TestDiffUsernames(t *testing.T) {
	gm := &GitHubManager{}

	got := gm.DiffUsernames([]string{"alice", "bob"}, []string{"alice", "carol", "dave"})
	want := []string{"carol", "dave"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("user %d = %q, want %q", i, got[i], want[i])
		}
	}
}
