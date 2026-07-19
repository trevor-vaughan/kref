package main

import (
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/store"
)

// newCacheRefreshCmd is the entry point the completion path spawns detached to
// warm the excerpt cache. Hidden: not part of the user-facing surface.
func newCacheRefreshCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:               "__cache-refresh",
		Hidden:            true,
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		SilenceErrors:     true,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			return s.RefreshAll()
		},
	}
}
