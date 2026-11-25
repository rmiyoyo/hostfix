package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type report struct {
	e        *Env
	warnings int
	failures int
}

func (r *report) section(title string) { r.e.say("\n%s", r.e.paint("1", title)) }

func (r *report) ok(format string, args ...any) {
	r.e.say("  %s %s", r.e.paint("32", "✓"), fmt.Sprintf(format, args...))
}

func (r *report) info(format string, args ...any) {
	r.e.say("  %s %s", r.e.paint("2", "·"), fmt.Sprintf(format, args...))
}

func (r *report) warn(msg, fix string) {
	r.warnings++
	r.problem("33", "!", msg, fix)
}

func (r *report) fail(msg, fix string) {
	r.failures++
	r.problem("31", "✗", msg, fix)
}

func (r *report) problem(color, mark, msg, fix string) {
	r.e.say("  %s %s", r.e.paint(color, mark), msg)
	if fix != "" {
		r.e.say("    %s %s", r.e.paint("2", "fix:"), fix)
	}
}

func doctor(e *Env, dir string) int {
	r := &report{e: e}
	e.say("hostfix %s checking %s", version, tildePath(e, dir))
	checkShell(e, r)
	info := checkPHP(e, r, dir)
	checkComposer(e, r, dir, info)
	project := checkNode(e, r, dir)
	checkLimits(e, r, project)
	checkFiles(e, r, dir)
	checkLaravel(e, r, dir)

	e.say("")
	switch {
	case r.failures > 0:
		e.say("%s and %s. Run `hostfix fix` to fix what can be fixed automatically.", plural(r.failures, "problem"), plural(r.warnings, "warning"))
		return 1
	case r.warnings > 0:
		e.say("No problems, %s.", plural(r.warnings, "warning"))
	default:
		e.say("Everything looks right.")
	}
	return 0
}

func checkShell(e *Env, r *report) {
	r.section("Shell")
	inPath := slices.Contains(filepath.SplitList(e.Path), e.binDir())
	switch {
	case !shellConfigured(e):
		r.warn("~/.bashrc does not put ~/.local/bin first in PATH, so tools installed there are not found", "hostfix shell")
	case !inPath:
		r.warn("~/.local/bin is set up but not active in this session", "source ~/.bashrc")
	default:
		r.ok("~/.local/bin is in PATH")
	}
}

var riskyFunctions = []struct{ name, breaks string }{
	{"proc_open", "Composer scripts, Laravel queue workers and the Process facade"},
	{"symlink", "php artisan storage:link (use `hostfix laravel storage-link`)"},
	{"putenv", "Composer and .env loading"},
	{"exec", "packages that call other programs"},
	{"shell_exec", "packages that call other programs"},
	{"popen", "mail and image tools"},
}

func checkPHP(e *Env, r *report, dir string) *PHPInfo {
	r.section("PHP")
	bins := discoverPHP(e)
	if len(bins) == 0 {
		r.info("No ea-php or alt-php under /opt, so this may not be a cPanel or CloudLinux server")
	} else {
		r.ok("Installed versions: %s", joinVersions(bins))
	}
	php := e.lookPath("php")
	if php == "" {
		r.fail("php is not in PATH", "hostfix php use auto")
		return nil
	}
	info, err := probePHP(e, php)
	if err != nil {
		r.fail(err.Error(), "hostfix php use auto")
		return nil
	}
	r.ok("Command line PHP %s (%s)", info.Version, tildePath(e, php))
	if web, source := webPHP(e, dir); web != "" {
		if web != info.Version.MajorMinor() {
			r.warn(fmt.Sprintf("The website runs PHP %s (%s) but the command line runs %s. Composer and artisan use the command line one.", web, source, info.Version.MajorMinor()),
				"hostfix php use "+web+"   or   hostfix php web "+info.Version.MajorMinor())
		} else {
			r.ok("The website runs the same version (%s)", source)
		}
	}
	var hits []string
	for _, fn := range riskyFunctions {
		if slices.Contains(info.Disabled, fn.name) {
			hits = append(hits, fmt.Sprintf("%s breaks %s", fn.name, fn.breaks))
		}
	}
	if len(hits) > 0 {
		r.warn("Disabled PHP functions on the command line:\n      "+strings.Join(hits, "\n      "),
			"ask your host to allow them for SSH, or try another version with `hostfix php use`")
	}
	if limit := parseBytes(info.MemoryLimit); limit > 0 && limit < 512<<20 {
		r.info("memory_limit is %s. The hostfix Composer wrapper lifts it for Composer.", info.MemoryLimit)
	}
	return info
}

func checkComposer(e *Env, r *report, dir string, info *PHPInfo) {
	req, err := readPHPRequirements(dir)
	if err != nil {
		r.section("Composer")
		r.fail(err.Error(), "")
		return
	}
	composer := e.lookPath("composer")
	if req == nil && composer == "" {
		return
	}
	r.section("Composer")
	switch v, ok := composerVersion(e, composer); {
	case composer == "":
		r.warn("composer is not in PATH", "hostfix composer")
	case !ok && info == nil:
		r.warn("composer is in PATH but cannot run without a working command line PHP", "hostfix php use auto")
	case !ok:
		r.warn("composer is in PATH but does not run", "hostfix composer")
	case v[0] < 2:
		r.fail(fmt.Sprintf("Composer %s is too old for current packages", v), "hostfix composer")
	default:
		r.ok("Composer %s", v)
	}
	if req == nil || info == nil {
		return
	}
	problems := req.problems(info)
	if len(problems) == 0 {
		r.ok("PHP %s meets every requirement in composer.json and composer.lock", info.Version.MajorMinor())
		return
	}
	fix := "enable the extensions in cPanel > Select PHP Version, or ask your host"
	if best, err := pickPHP(e, discoverPHP(e), req); err == nil {
		fix = fmt.Sprintf("hostfix php use auto   (picks PHP %s)", best.Version)
	}
	r.fail(fmt.Sprintf("PHP %s does not meet this project's requirements:\n      %s", info.Version, strings.Join(problems, "\n      ")), fix)
}

func checkNode(e *Env, r *report, dir string) *nodeProject {
	project, err := readNodeProject(dir)
	node := e.lookPath("node")
	if project == nil && node == "" && err == nil {
		return nil
	}
	r.section("Node.js")
	if err != nil {
		r.fail(err.Error(), "")
	}
	if node == "" {
		r.fail("node is not in PATH", "hostfix node install auto")
		return project
	}
	out, err := e.Exec(node, "-v")
	if err != nil {
		msg := "node does not run: " + firstLine(out, err)
		if strings.Contains(out, "GLIBC_") {
			msg = "This Node.js build needs a newer glibc than the server has: " + firstLine(out, err)
		}
		r.fail(msg, "hostfix node install auto")
		return project
	}
	v, _ := parseVersion(out)
	r.ok("Node.js %s (%s)", v, tildePath(e, node))
	if project != nil && project.Engine != "" && !satisfies(project.Engine, v) {
		r.fail(fmt.Sprintf("package.json needs Node.js %s", project.Engine), "hostfix node install auto")
	}
	if cache := npmCache(e); cache != "" && !writable(cache) {
		r.fail(fmt.Sprintf("npm cache %s is not writable (EACCES during npm install)", cache), "hostfix fix")
	}
	return project
}

func npmCache(e *Env) string {
	npm := e.lookPath("npm")
	if npm == "" {
		return ""
	}
	out, err := e.Exec(npm, "config", "get", "cache")
	if err != nil || !exists(out) {
		return ""
	}
	return out
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".hostfix-check-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func checkLimits(e *Env, r *report, project *nodeProject) {
	r.section("Limits")
	if mem := memoryLimit(e); mem > 0 {
		if project != nil && project.Build && mem < 2<<30 {
			r.warn(fmt.Sprintf("Only %s of memory. `npm run build` may be killed or fail with \"JavaScript heap out of memory\".", humanBytes(mem)),
				fmt.Sprintf("build on your computer or in CI and upload the output, or run NODE_OPTIONS=--max-old-space-size=%d npm run build", mem*3/4>>20))
		} else {
			r.ok("Memory limit %s", humanBytes(mem))
		}
	}
	if out, err := e.Exec("sh", "-c", "ulimit -u"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			if n < 100 {
				r.warn(fmt.Sprintf("Process limit is %d. npm, Composer and builds can fail with EAGAIN or \"fork: Resource temporarily unavailable\".", n),
					"run fewer things at once, or ask your host for a higher limit")
			} else {
				r.ok("Process limit %d", n)
			}
		}
	}
}

func memoryLimit(e *Env) int64 {
	var best int64
	consider := func(n int64) {
		if n > 0 && n < 1<<50 && (best == 0 || n < best) {
			best = n
		}
	}
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		if data, err := os.ReadFile(e.sys(path)); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				consider(n)
			}
		}
	}
	if data, err := os.ReadFile(e.sys("/proc/meminfo")); err == nil {
		if m := regexp.MustCompile(`MemTotal:\s+(\d+) kB`).FindSubmatch(data); m != nil {
			n, _ := strconv.ParseInt(string(m[1]), 10, 64)
			consider(n << 10)
		}
	}
	return best
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" || s == "-1" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult = 1 << 10
	case 'M':
		mult = 1 << 20
	case 'G':
		mult = 1 << 30
	}
	n, err := strconv.ParseInt(strings.TrimRight(s, "KMG"), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

func checkFiles(e *Env, r *report, dir string) {
	r.section("Files")
	if dir == e.Home {
		r.info("Run doctor inside a project folder to check its permissions")
		return
	}
	changes, err := planPerms(dir)
	if err != nil {
		r.fail(err.Error(), "")
		return
	}
	counts := map[string]int{}
	for _, c := range changes {
		counts[c.Reason]++
	}
	if counts[reasonWritable] > 0 {
		r.fail(fmt.Sprintf("%s: %s", plural(counts[reasonWritable], "path"), reasonWritable), "hostfix perms")
	}
	if counts[reasonOwner] > 0 {
		r.warn(fmt.Sprintf("%s: %s", plural(counts[reasonOwner], "path"), reasonOwner), "hostfix perms")
	}
	if counts[reasonBin] > 0 {
		r.fail(fmt.Sprintf("%s: %s", plural(counts[reasonBin], "path"), reasonBin), "hostfix perms")
	}
	if len(changes) == 0 {
		r.ok("Permissions look right")
	}
}

var cachedPath = regexp.MustCompile(`'(/[^']*?)/storage/`)

func checkLaravel(e *Env, r *report, dir string) {
	if !isLaravel(dir) {
		return
	}
	r.section("Laravel")
	env, err := readDotEnv(dir)
	if err != nil {
		r.fail(".env is missing", "cp .env.example .env && php artisan key:generate")
	} else {
		if env["APP_KEY"] == "" {
			r.fail("APP_KEY is empty (\"No application encryption key has been specified\")", "php artisan key:generate")
		}
		if strings.EqualFold(env["APP_DEBUG"], "true") && env["APP_ENV"] == "production" {
			r.warn("APP_DEBUG=true in production shows error details and secrets to visitors", "set APP_DEBUG=false in .env")
		}
		if env["APP_KEY"] != "" {
			r.ok(".env is present with an APP_KEY")
		}
	}
	if storageLinkMissing(dir) {
		r.warn("public/storage is not linked, so uploaded files return 404", "hostfix laravel storage-link")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "bootstrap", "cache", "config.php")); err == nil {
		real, _ := filepath.EvalSymlinks(dir)
		for _, m := range cachedPath.FindAllStringSubmatch(string(data), 5) {
			if m[1] != dir && m[1] != real {
				r.fail(fmt.Sprintf("bootstrap/cache/config.php points at %s, so it was cached on another machine (\"failed to open stream\" errors)", m[1]),
					"php artisan config:clear")
				break
			}
		}
	}
}
