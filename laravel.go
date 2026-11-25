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
		return errors.New("usage: hostfix laravel storage-link [dir] | docroot [--yes] [--force] <dir>")
	}
	switch rest[0] {
	case "storage-link":
		return storageLink(e, dirArg(rest[1:]))
	case "docroot":
		if len(rest) < 2 {
			return errors.New("usage: hostfix laravel docroot [--yes] <dir>")
		}
		return docroot(e, dirArg(rest[1:]), flags["yes"] || flags["y"], flags["force"])
	}
	return fmt.Errorf("unknown laravel command %q", rest[0])
}

func storageLinkMissing(dir string) bool {
	if !isLaravel(dir) {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "public", "storage"))
	return err != nil
}

func storageLink(e *Env, dir string) error {
	if !isLaravel(dir) {
		return fmt.Errorf("%s is not a Laravel project (no artisan file)", dir)
	}
	link := filepath.Join(dir, "public", "storage")
	target := filepath.Join(dir, "storage", "app", "public")
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a link; move it away first", tildePath(e, link))
		}
		if _, err := os.Stat(link); err == nil {
			e.done("public/storage is already linked")
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		e.say("  Removed a broken public/storage link")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	e.done("Linked public/storage to storage/app/public")
	e.say("  This is what `php artisan storage:link` does, without needing PHP's symlink() function")
	return nil
}

func otherSites(docroot string) []string {
	entries, err := os.ReadDir(docroot)
	if err != nil {
		return nil
	}
	var sites []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, marker := range []string{"index.php", "index.html", "index.htm", "wp-config.php"} {
			if exists(filepath.Join(docroot, entry.Name(), marker)) {
				sites = append(sites, entry.Name())
				break
			}
		}
	}
	return sites
}

func docroot(e *Env, dir string, yes, force bool) error {
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
	moved := uniquePath(e, docroot)
	sites := otherSites(docroot)
	if !yes {
		e.say("This will:")
		if exists(docroot) {
			e.say("  1. Rename ~/public_html to %s", tildePath(e, moved))
			e.say("  2. Make ~/public_html a link to %s", tildePath(e, public))
		} else {
			e.say("  1. Make ~/public_html a link to %s", tildePath(e, public))
		}
		e.say("Your main domain will then serve the Laravel app. Run again with --yes to do it.")
	}
	if len(sites) > 0 {
		e.note("These folders in ~/public_html look like other websites: %s", strings.Join(sites, ", "))
		e.say("  Addon domains and subdomains served from them stop working when public_html moves.")
		e.say("  Point this domain at %s in cPanel > Domains instead, or pass --force if they are not in use.", tildePath(e, public))
		if yes && !force {
			return fmt.Errorf("not moving ~/public_html while it holds other sites")
		}
	}
	if !yes {
		return nil
	}
	if exists(docroot) {
		if err := os.Rename(docroot, moved); err != nil {
			return err
		}
		e.say("  Moved the old ~/public_html to %s", tildePath(e, moved))
	}
	if err := os.Symlink(public, docroot); err != nil {
		if exists(moved) {
			if rerr := os.Rename(moved, docroot); rerr != nil {
				return fmt.Errorf("%v, and restoring ~/public_html from %s failed: %v", err, tildePath(e, moved), rerr)
			}
		}
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
		values[strings.TrimSpace(strings.TrimPrefix(key, "export "))] = envValue(value)
	}
	return values, scanner.Err()
}

func envValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value != "" && (value[0] == '"' || value[0] == '\'') {
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			return value[1 : end+1]
		}
		return strings.Trim(value, `"'`)
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}
