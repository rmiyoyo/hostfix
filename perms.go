package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type permChange struct {
	Path   string
	From   fs.FileMode
	To     fs.FileMode
	Reason string
}

const (
	reasonForeign  = "owned by another user, so hostfix cannot change them (ask your host to give them back to you)"
	reasonBin      = "node_modules/.bin tool is not executable (\"Permission denied\")"
	reasonWritable = "group or world writable (suEXEC and suPHP refuse these with a 500 error)"
	reasonWeb      = "not readable by the web server (403 Forbidden for images, CSS and JS)"
	reasonOwner    = "not readable by you, or not writable where Laravel writes"
)

var reasonOrder = []string{reasonForeign, reasonBin, reasonWritable, reasonWeb, reasonOwner}

func fileOwner(info fs.FileInfo) (int, bool) {
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	uid := v.FieldByName("Uid")
	if !uid.IsValid() || !uid.CanUint() {
		return 0, false
	}
	return int(uid.Uint()), true
}

func underAny(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func planPerms(dir string) ([]permChange, error) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	var public, writableRoots []string
	traverse := map[string]bool{}
	if isLaravel(dir) {
		writableRoots = []string{filepath.Join(dir, "storage"), filepath.Join(dir, "bootstrap", "cache")}
		if p := filepath.Join(dir, "public"); exists(p) {
			public = append(public, p)
			traverse[dir] = true
		}
	}
	if filepath.Base(dir) == "public_html" {
		public = append(public, dir)
	}
	me := os.Getuid()

	var changes []permChange
	binTargets := map[string]bool{}
	type entry struct {
		path string
		info fs.FileInfo
	}
	var entries []entry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		inBin := filepath.Base(filepath.Dir(path)) == ".bin" && strings.Contains(path, "node_modules")
		if d.Type()&fs.ModeSymlink != 0 {
			if inBin {
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
		if inBin && info.Mode().IsRegular() {
			binTargets[path] = true
		}
		entries = append(entries, entry{path, info})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, en := range entries {
		mode := en.info.Mode()
		if !mode.IsDir() && !mode.IsRegular() {
			continue
		}
		perm := mode.Perm()
		want := perm &^ 0o022
		reason := ""
		if want != perm {
			reason = reasonWritable
		}
		owner, writable := 0o400, underAny(en.path, writableRoots)
		if mode.IsDir() {
			owner = 0o500
		}
		if writable {
			owner |= 0o200
		}
		if want|fs.FileMode(owner) != want {
			want |= fs.FileMode(owner)
			if reason == "" {
				reason = reasonOwner
			}
		}
		if underAny(en.path, public) || (mode.IsDir() && traverse[en.path]) {
			extra := fs.FileMode(0o004)
			if mode.IsDir() {
				extra = 0o001
			}
			if want&extra == 0 {
				want |= extra
				if reason == "" || reason == reasonOwner {
					reason = reasonWeb
				}
			}
		}
		if mode.IsRegular() && binTargets[en.path] && want&0o100 == 0 {
			want |= 0o100
			reason = reasonBin
		}
		if want == perm {
			continue
		}
		if uid, ok := fileOwner(en.info); ok && me >= 0 && uid != me {
			changes = append(changes, permChange{Path: en.path, From: perm, To: perm, Reason: reasonForeign})
			continue
		}
		changes = append(changes, permChange{Path: en.path, From: perm, To: want, Reason: reason})
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
	shown := 0
	for _, c := range changes {
		if c.Reason == reasonForeign {
			counts[c.Reason]++
			continue
		}
		if dryRun {
			if shown < 50 {
				e.say("  %s %o -> %o", tildePath(e, c.Path), c.From, c.To)
			}
			shown++
		} else if err := os.Chmod(c.Path, c.To); err != nil {
			e.note("could not change %s: %v", tildePath(e, c.Path), err)
			continue
		}
		counts[c.Reason]++
	}
	if dryRun && shown > 50 {
		e.say("  ...and %d more", shown-50)
	}
	verb := "Fixed"
	if dryRun {
		verb = "Would fix"
	}
	for _, reason := range reasonOrder {
		switch {
		case counts[reason] == 0:
		case reason == reasonForeign:
			e.note("%s: %s", plural(counts[reason], "path"), reason)
		default:
			e.done("%s %s: %s", verb, plural(counts[reason], "path"), reason)
		}
	}
	return nil
}
