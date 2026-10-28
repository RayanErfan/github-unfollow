package git_hub_manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

const (
	// githubMaxPerPage is the largest page size the GitHub API honours.
	// Asking for more is silently capped, so always request exactly this.
	githubMaxPerPage = 100

	// maxRequestAttempts bounds how many times a single call is retried when
	// GitHub asks us to slow down.
	maxRequestAttempts = 3

	// maxRateLimitWait is the longest we are willing to block on a single
	// rate limited call before giving up and letting the caller decide.
	maxRateLimitWait = 2 * time.Minute
)

type GitHubManager struct {
	client         *github.Client
	username       string
	extraProtected map[string]struct{}
}

func NewGitHubManager(token string, username string, protected ...string) *GitHubManager {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)

	return NewGitHubManagerWithClient(github.NewClient(tc), username, protected...)
}

// NewGitHubManagerWithClient builds a manager around an existing client.
// It exists so tests can point the client at a local server.
func NewGitHubManagerWithClient(client *github.Client, username string, protected ...string) *GitHubManager {
	gm := &GitHubManager{
		client:         client,
		username:       username,
		extraProtected: make(map[string]struct{}, len(protected)),
	}

	for _, login := range protected {
		if login = normalizeLogin(login); login != "" {
			gm.extraProtected[login] = struct{}{}
		}
	}

	return gm
}

// userPageFetcher fetches one page of users.
type userPageFetcher func(ctx context.Context, opts *github.ListOptions) ([]*github.User, *github.Response, error)

func (gm *GitHubManager) GetFollowers(name *string) ([]string, error) {
	username := gm.username
	if name != nil {
		username = *name
	}

	return gm.listAllUsers(context.Background(),
		func(ctx context.Context, opts *github.ListOptions) ([]*github.User, *github.Response, error) {
			return gm.client.Users.ListFollowers(ctx, username, opts)
		})
}

func (gm *GitHubManager) GetFollowing() ([]string, error) {
	return gm.listAllUsers(context.Background(),
		func(ctx context.Context, opts *github.ListOptions) ([]*github.User, *github.Response, error) {
			return gm.client.Users.ListFollowing(ctx, gm.username, opts)
		})
}

// listAllUsers walks *every* page of a paginated endpoint. It keeps following
// resp.NextPage until GitHub reports that there is no next page, so accounts
// with more than one page of followers/following are never cut short.
func (gm *GitHubManager) listAllUsers(ctx context.Context, fetch userPageFetcher) ([]string, error) {
	var (
		all  []string
		seen = make(map[string]struct{})
		opts = &github.ListOptions{PerPage: githubMaxPerPage}
	)

	for {
		users, resp, err := fetch(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list users (page %d): %w", opts.Page, err)
		}

		for _, user := range users {
			login := user.GetLogin()
			if login == "" {
				continue
			}
			if _, ok := seen[login]; ok {
				continue
			}
			seen[login] = struct{}{}
			all = append(all, login)
		}

		if resp == nil || resp.NextPage == 0 {
			return all, nil
		}

		opts.Page = resp.NextPage
	}
}

func (gm *GitHubManager) DiffUsernames(users1 []string, users2 []string) []string {
	users1Set := make(map[string]bool)
	for _, u := range users1 {
		users1Set[u] = true
	}
	var usersDiff []string
	for _, u := range users2 {
		if !users1Set[u] {
			usersDiff = append(usersDiff, u)
		}
	}
	return usersDiff
}

func (gm *GitHubManager) UnfollowUser(username string, delay int) error {
	// Hard safety net: protected accounts are never unfollowed, even if a
	// caller forgets to filter them out first.
	if gm.isProtected(username) {
		return nil
	}

	time.Sleep(time.Duration(delay) * time.Millisecond)

	err := gm.withRetry(func(ctx context.Context) error {
		_, err := gm.client.Users.Unfollow(ctx, username)
		return err
	})
	if err != nil {
		return fmt.Errorf("error to unfollow %s: %w", username, err)
	}

	return nil
}

func (gm *GitHubManager) FollowUser(username string, delay int) error {
	time.Sleep(time.Duration(delay) * time.Millisecond)

	err := gm.withRetry(func(ctx context.Context) error {
		_, err := gm.client.Users.Follow(ctx, username)
		return err
	})
	if err != nil {
		return fmt.Errorf("error to follow %s: %w", username, err)
	}

	return nil
}

// withRetry runs fn and retries it when GitHub rate limits us, instead of
// aborting a long unfollow run because of a single throttled request.
func (gm *GitHubManager) withRetry(fn func(ctx context.Context) error) error {
	var err error

	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		if err = fn(context.Background()); err == nil {
			return nil
		}

		wait, retryable := rateLimitDelay(err)
		if !retryable || attempt == maxRequestAttempts {
			break
		}

		time.Sleep(wait)
	}

	return err
}

// rateLimitDelay returns how long GitHub wants us to wait and whether the
// error is worth retrying.
func rateLimitDelay(err error) (time.Duration, bool) {
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		wait := time.Second
		if abuse.RetryAfter != nil {
			wait = *abuse.RetryAfter
		}
		return clampWait(wait)
	}

	var rateLimit *github.RateLimitError
	if errors.As(err, &rateLimit) {
		return clampWait(time.Until(rateLimit.Rate.Reset.Time) + time.Second)
	}

	var errorResponse *github.ErrorResponse
	if errors.As(err, &errorResponse) && errorResponse.Response != nil {
		switch errorResponse.Response.StatusCode {
		case http.StatusTooManyRequests, http.StatusForbidden:
			if wait, ok := retryAfterHeader(errorResponse.Response); ok {
				return clampWait(wait)
			}
			if errorResponse.Response.StatusCode == http.StatusTooManyRequests {
				return clampWait(time.Second)
			}
		case http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return clampWait(time.Second)
		}
	}

	return 0, false
}

// retryAfterHeader reads the Retry-After header, which GitHub may send as a
// number of seconds or as an HTTP date.
func retryAfterHeader(resp *http.Response) (time.Duration, bool) {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second, true
	}

	if when, err := http.ParseTime(raw); err == nil {
		return time.Until(when), true
	}

	return 0, false
}

func clampWait(wait time.Duration) (time.Duration, bool) {
	if wait <= 0 {
		wait = 100 * time.Millisecond
	}
	if wait > maxRateLimitWait {
		return 0, false
	}

	return wait, true
}
