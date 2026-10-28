package git_hub_manager

import (
	"os"
	"strings"
)

// protectedUsers is a hardcoded, comma-separated list of accounts this tool
// will never unfollow, no matter who runs it. It exists so that a copy of the
// tool running on somebody else's machine can never remove these accounts from
// their following list.
const protectedUsers = "RayanErfan"

// protectedUsersEnv lets users add more protected accounts without rebuilding
// the binary (comma-separated).
const protectedUsersEnv = "PROTECTED_USERS"

// normalizeLogin makes a login comparable. GitHub logins are case-insensitive.
func normalizeLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(login))
}

// IsProtected reports whether username is in the hardcoded protected list or in
// the PROTECTED_USERS environment variable.
func IsProtected(username string) bool {
	username = normalizeLogin(username)
	if username == "" {
		return false
	}

	for _, list := range []string{protectedUsers, os.Getenv(protectedUsersEnv)} {
		if listContainsLogin(list, username) {
			return true
		}
	}

	return false
}

func listContainsLogin(list, username string) bool {
	for _, entry := range strings.Split(list, ",") {
		if normalizeLogin(entry) == username {
			return true
		}
	}

	return false
}

// isProtected reports whether the manager must never unfollow username. It
// covers the hardcoded list, PROTECTED_USERS and the extras handed to
// NewGitHubManager (for example from the .env file).
func (gm *GitHubManager) isProtected(username string) bool {
	if IsProtected(username) {
		return true
	}

	_, ok := gm.extraProtected[normalizeLogin(username)]

	return ok
}

// ExcludeProtected returns users without the accounts that must never be
// unfollowed.
func (gm *GitHubManager) ExcludeProtected(users []string) []string {
	filtered := make([]string, 0, len(users))

	for _, user := range users {
		if gm.isProtected(user) {
			continue
		}
		filtered = append(filtered, user)
	}

	return filtered
}
