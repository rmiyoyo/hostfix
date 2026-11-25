package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const cpanelComposer = "/opt/cpanel/composer/bin/composer"

func composerPhar(e *Env) string {
	if own := filepath.Join(e.dataDir(), "composer.phar"); exists(own) {
		return own
	}
	if exists(e.sys(cpanelComposer)) {
		return e.sys(cpanelComposer)
	}
	return ""
}

func writeComposerWrapper(e *Env) error {
	phar := composerPhar(e)
	if phar == "" {
		return nil
	}
	script := fmt.Sprintf(`#!/bin/sh
%s
export COMPOSER_MEMORY_LIMIT="${COMPOSER_MEMORY_LIMIT:--1}"
PHP="$HOME/.local/bin/php"
[ -x "$PHP" ] || PHP=php
exec "$PHP" %s "$@"
`, wrapperMark, shellQuote(phar))
	if err := os.MkdirAll(e.binDir(), 0o755); err != nil {
		return err
	}
	path := filepath.Join(e.binDir(), "composer")
	if data, err := os.ReadFile(path); err == nil && string(data) == script {
		return nil
	}
	if info, err := os.Lstat(path); err == nil {
		if data, _ := os.ReadFile(path); info.Mode()&os.ModeSymlink != 0 || !strings.Contains(string(data), wrapperMark) {
			saved := uniquePath(e, path)
			if err := os.Rename(path, saved); err != nil {
				return err
			}
			e.say("  Your previous %s was moved to %s", tildePath(e, path), tildePath(e, saved))
		}
	}
	tmp := path + ".hostfix-tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

const wrapperMark = ": hostfix composer wrapper"

func composerInstall(e *Env) error {
	sums, err := fetch(e, e.ComposerURL+".sha256sum")
	if err != nil {
		return err
	}
	sum := checksumFor(sums, "composer.phar")
	if sum == "" {
		return fmt.Errorf("no checksum found at %s.sha256sum", e.ComposerURL)
	}
	dest := filepath.Join(e.dataDir(), "composer.phar")
	if err := downloadVerified(e, e.ComposerURL, sum, dest); err != nil {
		return err
	}
	if err := writeComposerWrapper(e); err != nil {
		return err
	}
	if err := setupShell(e, true); err != nil {
		return err
	}
	e.done("Installed the latest Composer at %s", tildePath(e, filepath.Join(e.binDir(), "composer")))
	e.say("  It runs on your command line PHP with no memory limit, so `composer install` no longer runs out of memory")
	return nil
}

var composerVersionPattern = regexp.MustCompile(`Composer (?:version )?(\d+\.\d+\.\d+)`)

func composerVersion(e *Env, bin string) (Version, bool) {
	out, err := e.Exec(bin, "--version", "--no-ansi")
	if err != nil {
		return Version{}, false
	}
	m := composerVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return Version{}, false
	}
	return parseVersion(m[1])
}
