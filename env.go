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

func (e *Env) pathFirst(dir string) {
	kept := []string{dir}
	for _, p := range filepath.SplitList(e.Path) {
		if p != "" && filepath.Clean(p) != filepath.Clean(dir) {
			kept = append(kept, p)
		}
	}
	e.Path = strings.Join(kept, string(os.PathListSeparator))
}

func (e *Env) inPath(dir string) bool {
	for _, p := range filepath.SplitList(e.Path) {
		if p != "" && filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".hostfix-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), mode)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

func uniquePath(e *Env, path string) string {
	base := path + ".hostfix-" + e.Now().Format("20060102-150405")
	name := base
	for i := 2; exists(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

func backup(e *Env, path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	name := uniquePath(e, path)
	return name, os.WriteFile(name, data, 0o600)
}

func moveAside(e *Env, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}
	name := uniquePath(e, path)
	return name, os.Rename(path, name)
}

func tildePath(e *Env, path string) string {
	if rest, ok := strings.CutPrefix(path, e.Home+string(os.PathSeparator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}
