package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Env struct {
	Root           string
	Home           string
	Path           string
	Out            io.Writer
	Err            io.Writer
	Exec           func(name string, args ...string) (string, error)
	HTTP           *http.Client
	NodeDist       string
	NodeUnofficial string
	ComposerURL    string
	Now            func() time.Time
	Color          bool
}

func newEnv() *Env {
	home, _ := os.UserHomeDir()
	return &Env{
		Root:           "/",
		Home:           home,
		Path:           os.Getenv("PATH"),
		Out:            os.Stdout,
		Err:            os.Stderr,
		Exec:           execCommand,
		HTTP:           &http.Client{Timeout: 15 * time.Minute},
		NodeDist:       "https://nodejs.org/dist",
		NodeUnofficial: "https://unofficial-builds.nodejs.org/download/release",
		ComposerURL:    "https://getcomposer.org/download/latest-stable/composer.phar",
		Now:            time.Now,
		Color:          isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "",
	}
}

func execCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (e *Env) sys(path string) string { return filepath.Join(e.Root, path) }
func (e *Env) binDir() string         { return filepath.Join(e.Home, ".local", "bin") }
func (e *Env) dataDir() string        { return filepath.Join(e.Home, ".local", "share", "hostfix") }

func (e *Env) paint(code, s string) string {
	if !e.Color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (e *Env) say(format string, args ...any) { fmt.Fprintf(e.Out, format+"\n", args...) }
func (e *Env) done(format string, args ...any) {
	fmt.Fprintf(e.Out, "%s %s\n", e.paint("32", "✓"), fmt.Sprintf(format, args...))
}
func (e *Env) note(format string, args ...any) {
	fmt.Fprintf(e.Out, "%s %s\n", e.paint("33", "!"), fmt.Sprintf(format, args...))
}

func (e *Env) lookPath(name string) string {
	for _, dir := range filepath.SplitList(e.Path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate
		}
	}
	return ""
}

func (e *Env) prependPath(dir string) {
	e.Path = dir + string(os.PathListSeparator) + e.Path
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func replaceSymlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".hostfix-tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if info, err := os.Lstat(link); err == nil && info.IsDir() {
		os.Remove(tmp)
		return fmt.Errorf("%s is a directory, not replacing it", link)
	}
	return os.Rename(tmp, link)
}

func writeFileKeepMode(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func backup(e *Env, path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	name := path + ".hostfix-" + e.Now().Format("20060102-150405")
	return name, os.WriteFile(name, data, 0o600)
}

func tildePath(e *Env, path string) string {
	if rest, ok := strings.CutPrefix(path, e.Home+string(os.PathSeparator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}
