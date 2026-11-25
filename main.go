package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var version = "dev"

const usage = `hostfix fixes PHP, Composer, Node.js and permission problems on cPanel and CloudLinux shared hosting.

Usage:
  hostfix doctor [dir]                     Check the server and a project for common problems
  hostfix fix [dir]                        Fix everything doctor can fix safely, then check again

  hostfix php list                         List installed PHP versions
  hostfix php use <version|auto> [dir]     Set the command line PHP (auto reads composer.json and composer.lock)
  hostfix php web <version> [dir]          Set the PHP version a site runs on (.htaccess)
  hostfix composer                         Install the latest Composer, running on your PHP with no memory limit
  hostfix node install <version|lts|auto> [dir]
                                           Install Node.js without root (auto reads package.json engines)
  hostfix perms [--dry-run] [dir]          Fix permissions behind 500 errors and "Permission denied"
  hostfix laravel storage-link [dir]       Link public/storage without PHP's symlink()
  hostfix laravel docroot [--yes] <dir>    Point ~/public_html at a Laravel public/ folder
  hostfix cron [dir]                       Print a cron line that runs the right PHP
  hostfix shell                            Put ~/.local/bin first in PATH for SSH sessions
  hostfix version

dir defaults to the current folder.
`

func main() {
	os.Exit(run(newEnv(), os.Args[1:]))
}

func run(e *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(e.Out, usage)
		return 0
	}
	flags, rest := splitFlags(args[1:])
	allowed := map[string][]string{"perms": {"dry-run", "n"}, "laravel": {"yes", "y", "force"}}
	for flag := range flags {
		if !slices.Contains(allowed[args[0]], flag) {
			fmt.Fprintf(e.Err, "hostfix: unknown option --%s for %q\n", flag, args[0])
			return 2
		}
	}
	if needsDir[args[0]] {
		if err := checkDir(dirArg(rest)); err != nil {
			fmt.Fprintf(e.Err, "%s %v\n", e.paint("31", "hostfix:"), err)
			return 2
		}
	}
	var err error
	switch args[0] {
	case "doctor":
		return doctor(e, dirArg(rest))
	case "fix":
		return fix(e, dirArg(rest))
	case "php":
		err = phpCommand(e, rest)
	case "composer":
		err = composerInstall(e)
	case "node":
		if len(rest) < 2 || rest[0] != "install" {
			err = errors.New("usage: hostfix node install <version|lts|auto> [dir]")
		} else {
			err = nodeInstall(e, rest[1], dirArg(rest[2:]))
		}
	case "perms":
		err = fixPerms(e, dirArg(rest), flags["dry-run"] || flags["n"])
	case "laravel":
		err = laravelCommand(e, args[1:])
	case "cron":
		err = printCron(e, dirArg(rest))
	case "shell":
		err = setupShell(e, false)
	case "version", "--version", "-v":
		fmt.Fprintln(e.Out, version)
	case "help", "--help", "-h":
		fmt.Fprint(e.Out, usage)
	default:
		fmt.Fprintf(e.Err, "hostfix: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(e.Err, "%s %v\n", e.paint("31", "hostfix:"), err)
		return 1
	}
	return 0
}

func splitFlags(args []string) (map[string]bool, []string) {
	flags := map[string]bool{}
	var rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags[strings.TrimLeft(a, "-")] = true
		} else {
			rest = append(rest, a)
		}
	}
	return flags, rest
}

var needsDir = map[string]bool{"doctor": true, "fix": true, "perms": true, "cron": true}

func checkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s does not exist", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	return nil
}

func dirArg(args []string) string {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func printCron(e *Env, dir string) error {
	php := "/usr/local/bin/php"
	if target, err := filepath.EvalSymlinks(filepath.Join(e.binDir(), "php")); err == nil {
		php = target
	} else if bins := discoverPHP(e); len(bins) > 0 {
		php = bins[len(bins)-1].Path
	}
	if isLaravel(dir) {
		e.say("* * * * * cd %s && %s artisan schedule:run >> /dev/null 2>&1", shellQuote(dir), shellQuote(php))
	} else {
		e.say("*/5 * * * * %s %s >> /dev/null 2>&1", shellQuote(php), shellQuote(filepath.Join(dir, "your-script.php")))
	}
	e.say("")
	e.say("Paste this in cPanel > Cron Jobs. Cron does not read ~/.bashrc, so it needs the full PHP path;")
	e.say("a plain `php` there runs the server default version instead of yours.")
	return nil
}
