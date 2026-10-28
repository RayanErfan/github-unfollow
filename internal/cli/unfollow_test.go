package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/dbunt1tled/github-unfollow/internal/config"
	"github.com/dbunt1tled/github-unfollow/internal/git_hub_manager"
	"github.com/google/go-github/v57/github"
)

// fakeGitHub keeps a mutable following list and records unfollow calls.
type fakeGitHub struct {
	mu        sync.Mutex
	following []string
	deleted   []string
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/users/me/following", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		page := queryInt(r, "page", 1)
		perPage := queryInt(r, "per_page", 100)

		start := (page - 1) * perPage
		end := start + perPage
		if start > len(f.following) {
			start = len(f.following)
		}
		if end > len(f.following) {
			end = len(f.following)
		}

		if end < len(f.following) {
			w.Header().Set("Link", fmt.Sprintf(
				`<http://%s/users/me/following?per_page=%d&page=%d>; rel="next"`,
				r.Host, perPage, page+1,
			))
		}

		writeLogins(t, w, f.following[start:end])
	})

	mux.HandleFunc("/users/me/followers", func(w http.ResponseWriter, _ *http.Request) {
		writeLogins(t, w, nil)
	})

	mux.HandleFunc("/user/following/", func(w http.ResponseWriter, r *http.Request) {
		login := r.URL.Path[len("/user/following/"):]

		f.mu.Lock()
		defer f.mu.Unlock()

		for i, existing := range f.following {
			if existing == login {
				f.following = append(f.following[:i], f.following[i+1:]...)
				f.deleted = append(f.deleted, login)
				break
			}
		}

		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func writeLogins(t *testing.T, w http.ResponseWriter, logins []string) {
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

func TestUnfollowEveryPageUnfollowsBeyondFirstPage(t *testing.T) {
	const total = 250 // more than two full pages of 100

	state := &fakeGitHub{}
	for i := 0; i < total; i++ {
		state.following = append(state.following, fmt.Sprintf("user-%03d", i))
	}

	srv := httptest.NewServer(state.handler(t))
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	client.BaseURL = base

	gm := git_hub_manager.NewGitHubManagerWithClient(client, "me")
	cfg := &config.Config{WorkerCount: 4, QueueSize: 10, TimeDelay: 0}

	if err := unfollowEveryPage(gm, cfg); err != nil {
		t.Fatalf("unfollowEveryPage: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if len(state.following) != 0 {
		t.Fatalf("%d users were left following, want 0", len(state.following))
	}
	if len(state.deleted) != total {
		t.Fatalf("unfollowed %d users, want %d", len(state.deleted), total)
	}
}
