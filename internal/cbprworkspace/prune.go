package cbprworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// PruneGenerations removes the oldest valid inactive generations. It is
// intentionally opt-in: confirm must be true before any deletion occurs.
func PruneGenerations(workspace string, keep int, confirm bool) (int, error) {
	if !confirm {
		return 0, errors.New("generation pruning requires explicit confirmation")
	}
	if keep < 1 {
		return 0, errors.New("generation retention must be at least 1")
	}
	mu := workspaceLock(workspace)
	mu.Lock()
	defer mu.Unlock()
	root, err := realWorkspaceRoot(workspace)
	if err != nil {
		return 0, err
	}
	activeRoot, err := activeWorkspaceRoot(root)
	if err != nil {
		return 0, err
	}
	active := filepath.Base(activeRoot)
	dir := filepath.Join(root, GenerationsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		name string
		mod  int64
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.Name() == active || !generationNameRE.MatchString(entry.Name()) {
			continue
		}
		info, e := entry.Info()
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		manifest, e := loadManifestAt(filepath.Join(dir, entry.Name()))
		if e != nil || manifest.Fingerprint != entry.Name() {
			continue
		}
		if _, e = validatePublishedGeneration(filepath.Join(dir, entry.Name()), manifest); e != nil {
			continue
		}
		candidates = append(candidates, candidate{entry.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod < candidates[j].mod })
	retainInactive := keep - 1
	if retainInactive < 0 {
		retainInactive = 0
	}
	removed := 0
	if len(candidates) > retainInactive {
		remove := candidates[:len(candidates)-retainInactive]
		err = withPublicationLock(root, func() error {
			for _, c := range remove {
				if e := os.RemoveAll(filepath.Join(dir, c.name)); e != nil {
					return fmt.Errorf("removing generation %s: %w", c.name, e)
				}
				removed++
			}
			return syncGenerationDirectory(dir)
		})
	}
	return removed, err
}
