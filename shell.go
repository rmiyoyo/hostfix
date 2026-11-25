package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	blockBegin    = "# >>> hostfix >>>"
	blockEnd      = "# <<< hostfix <<<"
	oldBlockBegin = "# >>> server-env-setup >>>"
	oldBlockEnd   = "# <<< server-env-setup <<<"
)

const shellBlock = blockBegin + `
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
case "$PATH" in
    "$HOME/.local/bin:"*) ;;
    *) export PATH="$HOME/.local/bin:$PATH" ;;
esac
` + blockEnd + "\n"

const loginLine = `[ -n "$BASH_VERSION" ] && [ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"`

var (
	oldAlias      = regexp.MustCompile(`^alias (php|composer)=.*/opt/cpanel/`)
	sourcesBashrc = regexp.MustCompile(`(?m)^[^#\n]*(?:^|[\s;&|])(?:\.|source)\s+["']?(?:\$HOME|~|\$\{HOME\})/\.bashrc`)
)

func setupShell(e *Env, quiet bool) error {
	bashrc := filepath.Join(e.Home, ".bashrc")
	data, err := os.ReadFile(bashrc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	kept, err := stripBlock(string(data), oldBlockBegin, oldBlockEnd)
	if err == nil {
		kept, err = stripBlock(strings.Join(kept, "\n"), blockBegin, blockEnd)
	}
	if err != nil {
		return errors.New("~/.bashrc: " + err.Error())
	}
	kept = slicesDelete(kept, func(line string) bool { return oldAlias.MatchString(strings.TrimSpace(line)) })
	content := shellBlock
	if len(kept) > 0 {
		content = strings.Join(kept, "\n") + "\n\n" + shellBlock
	}
	changed := false
	if content != string(data) {
		if _, err := backup(e, bashrc); err != nil {
			return err
		}
		if err := writeFileAtomic(bashrc, []byte(content), 0o644); err != nil {
			return err
		}
		changed = true
	}

	login, err := ensureLoginSourcesBashrc(e)
	if err != nil {
		return err
	}
	if old := filepath.Join(e.Home, "perl5", "bin", "php"); isOldSymlink(old) {
		os.Remove(old)
	}
	if err := os.MkdirAll(e.binDir(), 0o755); err != nil {
		return err
	}
	if !quiet || changed || login != "" {
		e.done("~/.local/bin comes first in PATH for new SSH sessions")
		if login != "" {
			e.say("  %s now loads ~/.bashrc, so login shells pick it up too", login)
		}
		if changed {
			e.say("  Open a new SSH session to use it")
		}
	}
	return nil
}

func slicesDelete(lines []string, drop func(string) bool) []string {
	out := lines[:0]
	for _, line := range lines {
		if !drop(line) {
			out = append(out, line)
		}
	}
	return out
}

func isOldSymlink(path string) bool {
	target, err := os.Readlink(path)
	return err == nil && strings.HasPrefix(target, "/opt/")
}

func ensureLoginSourcesBashrc(e *Env) (string, error) {
	candidates := []string{".bash_profile", ".bash_login", ".profile"}
	name := candidates[0]
	for _, c := range candidates {
		if exists(filepath.Join(e.Home, c)) {
			name = c
			break
		}
	}
	path := filepath.Join(e.Home, name)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if sourcesBashrc.Match(data) {
		return "", nil
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += loginLine + "\n"
	if _, err := backup(e, path); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "~/" + name, nil
}

func shellConfigured(e *Env) bool {
	data, err := os.ReadFile(filepath.Join(e.Home, ".bashrc"))
	return err == nil && strings.Contains(string(data), blockBegin)
}
