package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type permChange struct {
	Path   string
	From   fs.FileMode
	To     fs.FileMode
	Reason string
}

const (
	reasonWritable = "group or world writable (suEXEC and suPHP refuse these with a 500 error)"
	reasonOwner    = "not readable or writable by you"
	reasonBin      = "node_modules/.bin tool is not executable (\"Permission denied\")"
)

func planPerms(dir string) ([]permChange, error) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	modes := map[string]fs.FileMode{}
	var order []string
	binTargets := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if filepath.Base(filepath.Dir(path)) == ".bin" && strings.Contains(path, "node_modules") {
				if target, err := filepath.EvalSymlinks(path); err == nil && strings.HasPrefix(target, dir) {
					binTargets[target] = true
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		modes[path] = info.Mode()
		order = append(order, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var changes []permChange
	for _, path := range order {
		mode := modes[path]
		perm := mode.Perm()
		want := perm &^ 0o022
		reason := ""
		if want != perm {
			reason = reasonWritable
		}
		if mode.IsDir() {
			want |= 0o700
		} else {
			want |= 0o600
		}
		if reason == "" && want != perm&^0o022 {
			reason = reasonOwner
		}
		if mode.IsRegular() && binTargets[path] && want&0o100 == 0 {
			want |= 0o100
			reason = reasonBin
		}
		if want != perm {
			changes = append(changes, permChange{Path: path, From: perm, To: want, Reason: reason})
		}
	}
	return changes, nil
}

func fixPerms(e *Env, dir string, dryRun bool) error {
	changes, err := planPerms(dir)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		e.done("Permissions in %s look right", tildePath(e, dir))
		return nil
	}
	counts := map[string]int{}
	for i, c := range changes {
		if dryRun {
			if i < 50 {
				e.say("  %s %o -> %o", tildePath(e, c.Path), c.From, c.To)
			}
		} else if err := os.Chmod(c.Path, c.To); err != nil {
			e.note("could not change %s: %v", tildePath(e, c.Path), err)
			continue
		}
		counts[c.Reason]++
	}
	if dryRun && len(changes) > 50 {
		e.say("  ...and %d more", len(changes)-50)
	}
	verb := "Fixed"
	if dryRun {
		verb = "Would fix"
	}
	for _, reason := range []string{reasonWritable, reasonOwner, reasonBin} {
		if counts[reason] > 0 {
			e.done("%s %s: %s", verb, plural(counts[reason], "path"), reason)
		}
	}
	return nil
}
