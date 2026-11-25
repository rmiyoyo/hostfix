package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type PHPBinary struct {
	Version string
	Path    string
	Source  string
}

type PHPInfo struct {
	Version     Version
	Extensions  map[string]bool
	Disabled    []string
	MemoryLimit string
}

var phpLocations = []struct {
	glob   string
	re     *regexp.Regexp
	source string
}{
	{"/opt/cpanel/ea-php*/root/usr/bin/php", regexp.MustCompile(`ea-php(\d)(\d+)/`), "ea-php"},
	{"/opt/alt/php*/usr/bin/php", regexp.MustCompile(`/php(\d)(\d+)/usr/`), "alt-php"},
}

func discoverPHP(e *Env) []PHPBinary {
	seen := map[string]bool{}
	var found []PHPBinary
	for _, loc := range phpLocations {
		matches, _ := filepath.Glob(e.sys(loc.glob))
		for _, path := range matches {
			m := loc.re.FindStringSubmatch(filepath.ToSlash(path))
			if m == nil {
				continue
			}
			version := m[1] + "." + m[2]
			if seen[path] || !isExecutable(path) {
				continue
			}
			seen[path] = true
			found = append(found, PHPBinary{Version: version, Path: path, Source: loc.source})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, _ := parseVersion(found[i].Version)
		b, _ := parseVersion(found[j].Version)
		return compareVersions(a, b) < 0
	})
	return found
}

const probeMarker = "@@hostfix-probe@@"

const phpProbe = `echo "` + probeMarker + `\n", PHP_VERSION, "\n", implode(",", get_loaded_extensions()), "\n", ini_get("disable_functions"), "\n", ini_get("memory_limit"), "\n";`

func probePHP(e *Env, bin string) (*PHPInfo, error) {
	out, err := e.Exec(bin, "-r", phpProbe)
	if err != nil {
		return nil, fmt.Errorf("could not run %s: %s", bin, firstLine(out, err))
	}
	_, body, found := strings.Cut(strings.ReplaceAll(out, "\r", ""), probeMarker+"\n")
	if !found {
		return nil, fmt.Errorf("unexpected output from %s: %s", bin, firstLine(out, nil))
	}
	lines := strings.Split(body, "\n")
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	version, ok := parseVersion(lines[0])
	if !ok {
		return nil, fmt.Errorf("unexpected output from %s: %s", bin, firstLine(out, nil))
	}
	info := &PHPInfo{Version: version, Extensions: map[string]bool{}, MemoryLimit: strings.TrimSpace(lines[3])}
	for _, ext := range strings.Split(lines[1], ",") {
		if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
			info.Extensions[strings.ReplaceAll(ext, " ", "_")] = true
		}
	}
	for _, fn := range strings.Split(lines[2], ",") {
		if fn = strings.TrimSpace(fn); fn != "" {
			info.Disabled = append(info.Disabled, strings.ToLower(fn))
		}
	}
	return info, nil
}

func firstLine(out string, err error) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if line == "" && err != nil {
		return err.Error()
	}
	return line
}

func normalizePHPVersion(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, prefix := range []string{"ea-php", "alt-php", "php"} {
		s = strings.TrimPrefix(s, prefix)
	}
	if m := regexp.MustCompile(`^(\d)(\d+)$`).FindStringSubmatch(s); m != nil {
		return m[1] + "." + m[2]
	}
	if v, parts, _, ok := parsePartial(s); ok && parts >= 2 {
		return v.MajorMinor()
	}
	return s
}

type PHPRequirements struct {
	Constraints map[string]string
	Extensions  []string
}

type composerFile struct {
	Require map[string]string `json:"require"`
	Config  struct {
		Platform map[string]any `json:"platform"`
	} `json:"config"`
}

type composerLock struct {
	Packages    []composerPackage `json:"packages"`
	PackagesDev []composerPackage `json:"packages-dev"`
}

type composerPackage struct {
	Name    string            `json:"name"`
	Require map[string]string `json:"require"`
}

func readPHPRequirements(dir string) (*PHPRequirements, error) {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var root composerFile
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("composer.json: %w", err)
	}
	req := &PHPRequirements{Constraints: map[string]string{}}
	exts := map[string]bool{}
	add := func(source string, require map[string]string) {
		for name, constraint := range require {
			switch {
			case name == "php":
				req.Constraints[source] = constraint
			case strings.HasPrefix(name, "ext-"):
				exts[strings.ToLower(strings.TrimPrefix(name, "ext-"))] = true
			}
		}
	}
	add("composer.json", root.Require)
	if data, err := os.ReadFile(filepath.Join(dir, "composer.lock")); err == nil {
		var lock composerLock
		if err := json.Unmarshal(data, &lock); err == nil {
			for _, pkg := range append(lock.Packages, lock.PackagesDev...) {
				add(pkg.Name, pkg.Require)
			}
		}
	}
	for ext := range exts {
		if _, faked := root.Config.Platform["ext-"+ext]; !faked {
			req.Extensions = append(req.Extensions, ext)
		}
	}
	sort.Strings(req.Extensions)
	return req, nil
}

func (r *PHPRequirements) problems(info *PHPInfo) []string {
	var out []string
	sources := make([]string, 0, len(r.Constraints))
	for source := range r.Constraints {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		if !satisfies(r.Constraints[source], info.Version) {
			out = append(out, fmt.Sprintf("%s needs PHP %s", source, r.Constraints[source]))
		}
	}
	var missing []string
	for _, ext := range r.Extensions {
		if !hasExtension(info, ext) {
			missing = append(missing, ext)
		}
	}
	if len(missing) > 0 {
		out = append(out, "missing extensions: "+strings.Join(missing, ", "))
	}
	return out
}

func hasExtension(info *PHPInfo, ext string) bool {
	ext = strings.ReplaceAll(ext, "-", "_")
	return info.Extensions[ext] || (ext == "opcache" && info.Extensions["zend_opcache"])
}

func pickPHP(e *Env, bins []PHPBinary, req *PHPRequirements) (PHPBinary, error) {
	var reasons []string
	for i := len(bins) - 1; i >= 0; i-- {
		info, err := probePHP(e, bins[i].Path)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", bins[i].Version, err))
			continue
		}
		problems := req.problems(info)
		if len(problems) == 0 {
			return bins[i], nil
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s", bins[i].Version, strings.Join(problems, "; ")))
	}
	return PHPBinary{}, fmt.Errorf("no installed PHP version meets this project's requirements:\n  %s", strings.Join(reasons, "\n  "))
}

func phpCommand(e *Env, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hostfix php list | use <version|auto> [dir] | web <version> [dir]")
	}
	switch args[0] {
	case "list":
		return phpList(e)
	case "use":
		if len(args) < 2 {
			return errors.New("usage: hostfix php use <version|auto> [dir]")
		}
		_, err := phpUse(e, args[1], dirArg(args[2:]))
		return err
	case "web":
		if len(args) < 2 {
			return errors.New("usage: hostfix php web <version> [dir]")
		}
		return phpWeb(e, args[1], dirArg(args[2:]))
	}
	return fmt.Errorf("unknown php command %q", args[0])
}

func phpList(e *Env) error {
	bins := discoverPHP(e)
	if len(bins) == 0 {
		return errNoPHP
	}
	current := ""
	if target, err := filepath.EvalSymlinks(filepath.Join(e.binDir(), "php")); err == nil {
		current = target
	}
	for _, b := range bins {
		marker := " "
		if real, err := filepath.EvalSymlinks(b.Path); err == nil && real == current {
			marker = "*"
		}
		e.say("%s %-5s %-8s %s", marker, b.Version, b.Source, b.Path)
	}
	return nil
}

var errNoPHP = errors.New("no ea-php or alt-php installations found under /opt. Is this a cPanel or CloudLinux server?")

func phpUse(e *Env, want, dir string) (PHPBinary, error) {
	bins := discoverPHP(e)
	if len(bins) == 0 {
		return PHPBinary{}, errNoPHP
	}
	var chosen PHPBinary
	if want == "auto" {
		req, err := readPHPRequirements(dir)
		if err != nil {
			return chosen, err
		}
		if req == nil {
			req = &PHPRequirements{}
		}
		if chosen, err = pickPHP(e, bins, req); err != nil {
			return chosen, err
		}
	} else {
		version := normalizePHPVersion(want)
		i := slices.IndexFunc(bins, func(b PHPBinary) bool { return b.Version == version })
		if i < 0 {
			return chosen, fmt.Errorf("PHP %s is not installed. Installed: %s", version, joinVersions(bins))
		}
		chosen = bins[i]
	}
	link := filepath.Join(e.binDir(), "php")
	saved, err := moveAside(e, link)
	if err != nil {
		return chosen, err
	}
	if err := replaceSymlink(chosen.Path, link); err != nil {
		return chosen, err
	}
	if err := writeComposerWrapper(e); err != nil {
		return chosen, err
	}
	if err := setupShell(e, true); err != nil {
		return chosen, err
	}
	e.done("Command line PHP is now %s (%s)", chosen.Version, chosen.Path)
	if saved != "" {
		e.say("  Your previous %s was moved to %s", tildePath(e, link), tildePath(e, saved))
	}
	e.say("  This covers SSH, Composer and artisan. Cron jobs need the full path: run `hostfix cron`.")
	if web, source := webPHP(e, dir); web != "" && web != chosen.Version {
		e.note("The website in %s runs PHP %s (%s). Run `hostfix php web %s` to match.", tildePath(e, dir), web, source, chosen.Version)
	}
	return chosen, nil
}

func joinVersions(bins []PHPBinary) string {
	var out []string
	for _, b := range bins {
		if !slices.Contains(out, b.Version) {
			out = append(out, b.Version)
		}
	}
	return strings.Join(out, ", ")
}

var (
	handlerPattern  = regexp.MustCompile(`(?im)^\s*AddHandler\s+application/x-(?:httpd-(?:ea-|alt-)?|)(?:php|lsphp)(\d)(\d+)`)
	selectorPattern = regexp.MustCompile(`(?im)^\s*AddHandler\s+application/x-httpd-lsphp\s`)
	defaultPattern  = regexp.MustCompile(`"?default"?\s*[:=]\s*"?(?:ea-|alt-)?php(\d)(\d+)`)
)

func webDir(dir string) string {
	if public := filepath.Join(dir, "public"); isLaravel(dir) && exists(public) {
		return public
	}
	return dir
}

func webPHP(e *Env, dir string) (string, string) {
	for d := webDir(dir); ; {
		if data, err := os.ReadFile(filepath.Join(d, ".htaccess")); err == nil {
			if all := handlerPattern.FindAllStringSubmatch(string(data), -1); len(all) > 0 {
				m := all[len(all)-1]
				return m[1] + "." + m[2], tildePath(e, filepath.Join(d, ".htaccess"))
			}
			if selectorPattern.Match(data) {
				return "", "cPanel > Select PHP Version"
			}
		}
		parent := filepath.Dir(d)
		if d == e.Home || parent == d {
			break
		}
		d = parent
	}
	for _, conf := range []string{"/etc/cpanel/ea4/php.conf", "/etc/cpanel/ea4/ea4.conf"} {
		if data, err := os.ReadFile(e.sys(conf)); err == nil {
			if m := defaultPattern.FindSubmatch(data); m != nil {
				return string(m[1]) + "." + string(m[2]), "server default"
			}
		}
	}
	return "", ""
}

const (
	handlerBegin = "# php -- BEGIN cPanel-generated handler, do not edit"
	handlerEnd   = "# php -- END cPanel-generated handler, do not edit"
)

func stripBlock(content, begin, end string) ([]string, error) {
	var kept []string
	skip := false
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == begin:
			skip = true
		case trimmed == end:
			skip = false
		case !skip:
			kept = append(kept, line)
		}
	}
	if skip {
		return nil, fmt.Errorf("found %q without a matching end line; fix the file by hand first", begin)
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	return kept, nil
}

func phpWeb(e *Env, want, dir string) error {
	version := normalizePHPVersion(want)
	pkg := "ea-php" + strings.ReplaceAll(version, ".", "")
	if !exists(e.sys("/opt/cpanel/" + pkg)) {
		if exists(e.sys("/opt/alt/php" + strings.ReplaceAll(version, ".", ""))) {
			return fmt.Errorf("PHP %s is only available as CloudLinux alt-php here. Set it in cPanel > Select PHP Version", version)
		}
		return fmt.Errorf("%s is not installed on this server", pkg)
	}
	site := webDir(dir)
	path := filepath.Join(site, ".htaccess")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	kept, err := stripBlock(string(data), handlerBegin, handlerEnd)
	if err != nil {
		return fmt.Errorf("%s: %w", tildePath(e, path), err)
	}
	block := []string{
		handlerBegin,
		fmt.Sprintf("# Set the “%s” package as the default “PHP” programming language.", pkg),
		"<IfModule mime_module>",
		fmt.Sprintf("  AddHandler application/x-httpd-%s .php .php8 .phtml", pkg),
		"</IfModule>",
		handlerEnd,
	}
	if len(kept) > 0 {
		kept = append(kept, "")
	}
	out := strings.Join(append(kept, block...), "\n") + "\n"
	if out == string(data) {
		e.done("%s already runs PHP %s", tildePath(e, site), version)
		return nil
	}
	saved, err := backup(e, path)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(out), 0o644); err != nil {
		return err
	}
	e.done("The website in %s now runs PHP %s", tildePath(e, site), version)
	if saved != "" {
		e.say("  Previous .htaccess saved as %s", tildePath(e, saved))
	}
	return nil
}
