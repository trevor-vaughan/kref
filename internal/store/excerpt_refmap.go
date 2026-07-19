package store

import (
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// refMap is the tier's freshness fingerprint: entry id -> current tip commit.
// Same OID means byte-identical entry content (git is content-addressed), so a
// map comparison is an exact staleness test with no DAG read.
type refMap map[entity.Id]repository.Hash

func buildRefMap(repo repository.ClockedRepo, t entry.Tier) (refMap, error) {
	prefix := "refs/" + t.Namespace() + "/"
	refs, err := repo.ListRefs(prefix)
	if err != nil {
		return nil, err
	}
	m := make(refMap, len(refs))
	for _, ref := range refs {
		h, err := repo.ResolveRef(ref)
		if err != nil {
			return nil, err
		}
		id := entity.Id(ref[strings.LastIndex(ref, "/")+1:])
		m[id] = h
	}
	return m, nil
}

// refDiff reports ids to (re)compile (new or moved tip) and ids to drop
// (present in old, gone in cur).
func refDiff(old, cur refMap) (changed, removed []entity.Id) {
	for id, h := range cur {
		if old[id] != h {
			changed = append(changed, id)
		}
	}
	for id := range old {
		if _, ok := cur[id]; !ok {
			removed = append(removed, id)
		}
	}
	return changed, removed
}
