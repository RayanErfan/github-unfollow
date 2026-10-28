package cli

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dbunt1tled/github-unfollow/internal/config"
	"github.com/dbunt1tled/github-unfollow/internal/git_hub_manager"
	"github.com/dbunt1tled/github-unfollow/internal/helper"
	"github.com/dbunt1tled/github-unfollow/internal/worker"
	"github.com/spf13/cobra"
)

func NewUnFollowCommand() *cobra.Command {
	cmd := unFollowCommand()
	cmd.Flags().BoolP("force", "f", false, "Force unfollow")

	return cmd
}

func unFollowCommand() *cobra.Command {
	return &cobra.Command{ //nolint:gochecknoglobals // need for init command
		Use:   "unfollow",
		Short: "Unfollow users",
		Long:  "Unfollow users who not follow you",
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				err                              error
				force                            bool
				cfg                              *config.Config
				followers, toUnfollow, following []string
			)
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			force, err = cmd.Flags().GetBool("force")
			if err != nil {
				return fmt.Errorf("failed to get force flag: %w", err)
			}
			gm := git_hub_manager.NewGitHubManager(cfg.GitHubToken, cfg.GitHubUsername)
			followers, err = gm.GetFollowers(nil)
			if err != nil {
				return err
			}
			following, err = gm.GetFollowing()
			if err != nil {
				return err
			}

			toUnfollow = gm.DiffUsernames(followers, following)

			for i, user := range toUnfollow {
				fmt.Printf("%d. %s\n", i+1, user)
			}
			if len(toUnfollow) == 0 {
				fmt.Println("No users to unfollow")
				return nil
			}
			if force == false {
				fmt.Printf("\nYou are sure that you want to unfollow %d users?", len(toUnfollow))
				confirm := helper.GetInput("")
				if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
					fmt.Println("Operation canceled")
					return nil
				}
			}

			return unfollowEveryPage(gm, cfg)
		},
	}
}

// unfollowEveryPage unfollows everyone who does not follow back, walking every
// page of the following list instead of only the first one.
//
// It works in passes: each pass re-reads the full (paginated) follower and
// following lists, unfollows the users it has not tried yet, and repeats until
// a pass finds nothing new. Re-reading between passes keeps the tool correct
// even when the list shifts while it is being unfollowed, which is what used
// to leave accounts partially cleaned.
func unfollowEveryPage(gm *git_hub_manager.GitHubManager, cfg *config.Config) error {
	var (
		attempted  = make(map[string]struct{})
		unfollowed int
		failed     int
	)

	for pass := 1; ; pass++ {
		batch, err := pendingUnfollows(gm, attempted)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}

		fmt.Printf("\nPass %d: unfollowing %d users...\n", pass, len(batch))

		ok, bad := runUnfollowBatch(gm, cfg, batch, attempted)
		unfollowed += ok
		failed += bad

		// Retrying the same users over and over would loop forever when the
		// API keeps rejecting them.
		if ok == 0 {
			break
		}
	}

	fmt.Printf("\nDone. Unfollowed: %d, failed: %d\n", unfollowed, failed)

	if failed > 0 {
		return fmt.Errorf("%d users could not be unfollowed", failed)
	}

	return nil
}

// pendingUnfollows returns the users that do not follow back and have not been
// processed yet. Both lists are read completely, page by page.
func pendingUnfollows(gm *git_hub_manager.GitHubManager, attempted map[string]struct{}) ([]string, error) {
	followers, err := gm.GetFollowers(nil)
	if err != nil {
		return nil, err
	}
	following, err := gm.GetFollowing()
	if err != nil {
		return nil, err
	}

	diff := gm.DiffUsernames(followers, following)

	batch := make([]string, 0, len(diff))
	for _, user := range diff {
		if _, done := attempted[user]; done {
			continue
		}
		batch = append(batch, user)
	}

	return batch, nil
}

// runUnfollowBatch unfollows the batch concurrently and returns the number of
// successes and failures. A failing user never aborts the whole run.
func runUnfollowBatch(
	gm *git_hub_manager.GitHubManager,
	cfg *config.Config,
	batch []string,
	attempted map[string]struct{},
) (int, int) {
	var (
		mu         sync.Mutex
		unfollowed int
		failed     int
	)

	wp := worker.NewWorker(cfg.WorkerCount, cfg.QueueSize)
	wp.Start()

	for _, user := range batch {
		user := user
		attempted[user] = struct{}{}

		wp.AddTask(func() {
			if err := gm.UnfollowUser(user, cfg.TimeDelay); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()

				fmt.Printf("Error unfollow %s: %v\n", user, err)
				return
			}

			mu.Lock()
			unfollowed++
			mu.Unlock()

			fmt.Printf("UnFollowed %s\n", user)
		})
	}

	wp.Wait()
	wp.Stop()

	return unfollowed, failed
}
