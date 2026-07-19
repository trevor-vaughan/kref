package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/git-bug/git-bug/entity"
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/config"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
)

func newFavCmd(dir *string) *cobra.Command {
	c := &cobra.Command{
		Use:     "fav",
		Aliases: []string{"alt"},
		Short:   "Manage favorites (named shortcuts to entries)",
		Long:    "Favorites give an entry a memorable name you can use anywhere an id is accepted (kref show <name>, kref diff <name>, ...). They live in your user config; names must contain a non-hex character so they never shadow an id.",
	}

	var addShared, rmShared bool

	add := &cobra.Command{
		Use:     "add <id> <name>",
		Short:   "Add or update a favorite pointing at an entry",
		Example: exampleBlock([]string{"kref fav add a1b2c3d4 todo"}),
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[1]
			if err := config.ValidFavoriteName(name); err != nil {
				return err
			}
			s, err := store.Open(*dir)
			if err != nil {
				return err
			}
			defer s.Close()
			id, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			if addShared {
				body, entryID, ok := s.ProjectConfigEntry()
				if !ok {
					return errors.New("no project config entry — create it with `kref config init --shared`")
				}
				pc, err := config.Parse([]byte(body))
				if err != nil {
					return err
				}
				if pc.Favorites == nil {
					pc.Favorites = map[string]string{}
				}
				pc.Favorites[name] = id.String()
				if err := config.Validate(pc); err != nil {
					return err
				}
				newBody, err := config.MarshalEntry(pc)
				if err != nil {
					return err
				}
				if err := s.Update(entryID, string(newBody), ""); err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, _ bool) {
						fmt.Fprintf(w, "favorited %s -> %s (shared)\n", name, render.ShortID(id))
					},
					map[string]string{"status": "favorited", "name": name, "id": id.String(), "layer": "shared"})
			}
			if err := setUserFavorite(name, id); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) {
					fmt.Fprintf(w, "favorited %s -> %s\n", name, render.ShortID(id))
				},
				map[string]string{"status": "favorited", "name": name, "id": id.String()})
		},
	}
	add.ValidArgsFunction = entryArgs(dir, 1, sourceAll) // id at position 0; name (position 1) is free-form
	add.Flags().BoolVar(&addShared, "shared", false, "write to the shared project config entry instead of your user config")

	rm := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a favorite",
		Example: exampleBlock([]string{"kref fav rm todo"}),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if rmShared {
				s, err := store.Open(*dir)
				if err != nil {
					return err
				}
				defer s.Close()
				body, entryID, ok := s.ProjectConfigEntry()
				if !ok {
					return errors.New("no project config entry — create it with `kref config init --shared`")
				}
				pc, err := config.Parse([]byte(body))
				if err != nil {
					return err
				}
				if _, ok := pc.Favorites[name]; !ok {
					return fmt.Errorf("no shared favorite named %q", name)
				}
				delete(pc.Favorites, name)
				if err := config.Validate(pc); err != nil {
					return err
				}
				newBody, err := config.MarshalEntry(pc)
				if err != nil {
					return err
				}
				if err := s.Update(entryID, string(newBody), ""); err != nil {
					return err
				}
				return emit(cmd,
					func(w io.Writer, _ bool) { fmt.Fprintf(w, "removed favorite %s (shared)\n", name) },
					map[string]string{"status": "removed", "name": name, "layer": "shared"})
			}
			if err := removeUserFavorite(name); err != nil {
				return err
			}
			return emit(cmd,
				func(w io.Writer, _ bool) { fmt.Fprintf(w, "removed favorite %s\n", name) },
				map[string]string{"status": "removed", "name": name})
		},
	}
	rm.Flags().BoolVar(&rmShared, "shared", false, "remove from the shared project config entry instead of your user config")
	rm.ValidArgsFunction = favoriteArgs(dir, &rmShared)

	runFavList := func(cmd *cobra.Command, _ []string) error {
		s, err := store.Open(*dir)
		if err != nil {
			return err
		}
		defer s.Close()
		favs := s.Favorites()
		names := make([]string, 0, len(favs))
		for n := range favs {
			names = append(names, n)
		}
		sort.Strings(names)
		type row struct {
			Name   string `json:"name"`
			ID     string `json:"id"`
			Origin string `json:"origin"`
			Title  string `json:"title"`
		}
		rows := make([]row, 0, len(names))
		for _, n := range names {
			id := favs[n]
			title := ""
			if snap, gErr := s.Get(entity.Id(id)); gErr == nil {
				title = snap.Title
			}
			rows = append(rows, row{Name: n, ID: id, Origin: s.FavoriteOrigin(n), Title: title})
		}
		return emit(cmd,
			func(w io.Writer, _ bool) {
				for _, r := range rows {
					fmt.Fprintf(w, "%-16s (%s)\t%s\t%s\n", r.Name, r.Origin, render.ShortID(entity.Id(r.ID)), r.Title)
				}
			},
			rows)
	}

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List favorites (both layers)",
		Example: exampleBlock([]string{"kref fav ls"}),
		Args:    cobra.NoArgs,
		RunE:    runFavList,
	}

	// A bare `kref fav` lists favorites — the reach-for-it default — instead of
	// printing usage. NoArgs keeps an unknown token (e.g. `kref fav bogus`) an
	// error rather than silently listing.
	c.Args = cobra.NoArgs
	c.RunE = runFavList

	c.AddCommand(add, rm, ls)
	return c
}

// loadUserConfigForEdit reads the user config file for mutation, returning its
// path and parsed contents (a fresh InitialUserConfig when the file is absent).
func loadUserConfigForEdit() (string, *config.Config, error) {
	path, err := config.UserPath(os.Getenv)
	if err != nil {
		return "", nil, err
	}
	b, rerr := os.ReadFile(path)
	if os.IsNotExist(rerr) {
		return path, config.InitialUserConfig(), nil
	}
	if rerr != nil {
		return "", nil, rerr
	}
	c, perr := config.Parse(b)
	if perr != nil {
		return "", nil, perr
	}
	return path, c, nil
}

// setUserFavorite adds or updates a user-scope favorite name → id in the user
// config file. Shared by `kref fav add` and the interactive list cockpit. The
// name must contain a non-hex character so it never shadows an id.
func setUserFavorite(name string, id entity.Id) error {
	if err := config.ValidFavoriteName(name); err != nil {
		return err
	}
	path, uc, err := loadUserConfigForEdit()
	if err != nil {
		return err
	}
	if uc.Favorites == nil {
		uc.Favorites = map[string]string{}
	}
	uc.Favorites[name] = id.String()
	if err := config.Validate(uc); err != nil {
		return err
	}
	return config.WriteFile(path, uc)
}

// removeUserFavorite deletes a user-scope favorite name from the user config file.
func removeUserFavorite(name string) error {
	path, uc, err := loadUserConfigForEdit()
	if err != nil {
		return err
	}
	if _, ok := uc.Favorites[name]; !ok {
		return fmt.Errorf("no user favorite named %q", name)
	}
	delete(uc.Favorites, name)
	if err := config.Validate(uc); err != nil {
		return err
	}
	return config.WriteFile(path, uc)
}

// favoritesFor returns the favorite names that point at id, sorted.
func favoritesFor(favs map[string]string, id entity.Id) []string {
	var out []string
	for name, target := range favs {
		if target == id.String() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
