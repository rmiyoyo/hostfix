package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func isLaravel(dir string) bool {
	return exists(filepath.Join(dir, "artisan"))
}

func laravelCommand(e *Env, args []string) error {
	flags, rest := splitFlags(args)
	if len(rest) == 0 {
		return errors.New("usage: hostfix laravel storage-link [dir] | docroot [--yes] <dir>")
	}
	switch rest[0] {
	case "storage-link":
		return storageLink(e, dirArg(rest[1:]))
	case "docroot":
		if len(rest) < 2 {
			return errors.New("usage: hostfix laravel docroot [--yes] <dir>")
		}
		return docroot(e, dirArg(rest[1:]), flags["yes"] || flags["y"])
	}
	return fmt.Errorf("unknown laravel command %q", rest[0])
}

func storageLinkMissing(dir string) bool {
	return isLaravel(dir) && !exists(filepath.Join(dir, "public", "storage"))
}

func storageLink(e *Env, dir string) error {
	if !isLaravel(dir) {
		return fmt.Errorf("%s is not a Laravel project (no artisan file)", dir)
	}
	link := filepath.Join(dir, "public", "storage")
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			e.done("public/storage is already linked")
			return nil
		}
		return fmt.Errorf("%s exists and is not a link; move it away first", tildePath(e, link))
	}
	if err := os.MkdirAll(filepath.Join(dir, "storage", "app", "public"), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(filepath.Join("..", "storage", "app", "public"), link); err != nil {
		return err
	}
	e.done("Linked public/storage to storage/app/public")
	e.say("  This is what `php artisan storage:link` does, without needing PHP's symlink() function")
	return nil
}

func docroot(e *Env, dir string, yes bool) error {
	public := filepath.Join(dir, "public")
	if info, err := os.Stat(public); err != nil || !info.IsDir() {
		return fmt.Errorf("%s has no public/ folder", tildePath(e, dir))
	}
	docroot := filepath.Join(e.Home, "public_html")
	if target, err := filepath.EvalSymlinks(docroot); err == nil {
		if realPublic, _ := filepath.EvalSymlinks(public); target == realPublic {
			e.done("~/public_html already points at %s", tildePath(e, public))
			return nil
		}
	}
	moved := docroot + ".hostfix-" + e.Now().Format("20060102-150405")
	if !yes {
		e.say("This will:")
		if exists(docroot) {
			e.say("  1. Rename ~/public_html to %s", tildePath(e, moved))
			e.say("  2. Make ~/public_html a link to %s", tildePath(e, public))
		} else {
			e.say("  1. Make ~/public_html a link to %s", tildePath(e, public))
		}
		e.say("Your main domain will then serve the Laravel app. Run again with --yes to do it.")
		return nil
	}
	if exists(docroot) {
		if err := os.Rename(docroot, moved); err != nil {
			return err
		}
		e.say("  Moved the old ~/public_html to %s", tildePath(e, moved))
	}
	if err := os.Symlink(public, docroot); err != nil {
		return err
	}
	e.done("~/public_html now points at %s", tildePath(e, public))
	e.say("  Addon domains are set in cPanel > Domains, where you can point the document root at the public/ folder instead")
	return nil
}

func readDotEnv(dir string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(strings.TrimPrefix(key, "export "))] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, scanner.Err()
}
