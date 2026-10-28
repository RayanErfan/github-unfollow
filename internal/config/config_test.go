package config

import "testing"

// TestLoadReadsEnvironmentVariables guards the documented behaviour: token,
// username and protected accounts can be supplied through the environment
// instead of a .env file.
func TestLoadReadsEnvironmentVariables(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_USERNAME", "test-user")
	t.Setenv("PROTECTED_USERS", "alice,bob")
	t.Setenv("WORKER_COUNT", "2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.GitHubToken != "test-token" {
		t.Errorf("GitHubToken = %q, want %q", cfg.GitHubToken, "test-token")
	}
	if cfg.GitHubUsername != "test-user" {
		t.Errorf("GitHubUsername = %q, want %q", cfg.GitHubUsername, "test-user")
	}
	if cfg.WorkerCount != 2 {
		t.Errorf("WorkerCount = %d, want 2", cfg.WorkerCount)
	}
	if len(cfg.ProtectedUsers) != 2 || cfg.ProtectedUsers[0] != "alice" || cfg.ProtectedUsers[1] != "bob" {
		t.Errorf("ProtectedUsers = %v, want [alice bob]", cfg.ProtectedUsers)
	}
}
