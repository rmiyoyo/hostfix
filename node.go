package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

type nodeRelease struct {
	Version string   `json:"version"`
	LTS     any      `json:"lts"`
	Files   []string `json:"files"`
}

func (r nodeRelease) isLTS() bool {
	name, ok := r.LTS.(string)
	return ok && name != ""
}

func (r nodeRelease) parsed() Version {
	v, _ := parseVersion(r.Version)
	return v
}

func chooseNode(releases []nodeRelease, want, flavor string) (nodeRelease, error) {
	var candidates []nodeRelease
	for _, r := range releases {
		if !strings.Contains(r.Version, "-") && slices.Contains(r.Files, flavor) {
			candidates = append(candidates, r)
		}
	}
	slices.SortFunc(candidates, func(a, b nodeRelease) int { return compareVersions(b.parsed(), a.parsed()) })
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	pick := func(match func(nodeRelease) bool) (nodeRelease, bool) {
		i := slices.IndexFunc(candidates, match)
		if i < 0 {
			return nodeRelease{}, false
		}
		return candidates[i], true
	}
	switch {
	case want == "" || want == "lts":
		if r, ok := pick(nodeRelease.isLTS); ok {
			return r, nil
		}
	case regexp.MustCompile(`^\d+(\.\d+){0,2}$`).MatchString(want):
		if r, ok := pick(func(r nodeRelease) bool { return satisfies(want, r.parsed()) }); ok {
			return r, nil
		}
	default:
		if r, ok := pick(func(r nodeRelease) bool { return r.isLTS() && satisfies(want, r.parsed()) }); ok {
			return r, nil
		}
		if r, ok := pick(func(r nodeRelease) bool { return satisfies(want, r.parsed()) }); ok {
			return r, nil
		}
	}
	return nodeRelease{}, fmt.Errorf("no Node.js release matches %q for %s", want, flavor)
}

func nodeArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported CPU architecture %s", runtime.GOARCH)
}

func glibcVersion(e *Env) (Version, bool) {
	out, err := e.Exec("getconf", "GNU_LIBC_VERSION")
	if err != nil {
		out, _ = e.Exec("ldd", "--version")
	}
	m := regexp.MustCompile(`(\d+\.\d+)`).FindString(out)
	if m == "" {
		return Version{}, false
	}
	return parseVersion(m)
}

type nodeProject struct {
	Engine string
	Build  bool
}

func readNodeProject(dir string) (*nodeProject, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Engines map[string]string `json:"engines"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	_, build := pkg.Scripts["build"]
	return &nodeProject{Engine: pkg.Engines["node"], Build: build}, nil
}

func loadReleases(e *Env, base string) ([]nodeRelease, error) {
	data, err := fetch(e, base+"/index.json")
	if err != nil {
		return nil, err
	}
	var releases []nodeRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, fmt.Errorf("could not read %s/index.json: %w", base, err)
	}
	return releases, nil
}

type nodeSource struct {
	base   string
	flavor string
}

func nodeInstall(e *Env, want, dir string) error {
	if want == "auto" {
		project, err := readNodeProject(dir)
		if err != nil {
			return err
		}
		want = "lts"
		if project != nil && project.Engine != "" {
			want = project.Engine
		}
	}
	arch, err := nodeArch()
	if err != nil {
		return err
	}
	official := nodeSource{e.NodeDist, "linux-" + arch}
	legacy := nodeSource{e.NodeUnofficial, "linux-x64-glibc-217"}
	sources := []nodeSource{official}
	if arch == "x64" {
		sources = append(sources, legacy)
	}
	if glibc, ok := glibcVersion(e); ok && compareVersions(glibc, Version{2, 28}) < 0 {
		if arch != "x64" {
			return fmt.Errorf("this server has glibc %s, too old for Node.js 18+, and no compatible build exists for %s", glibc.MajorMinor(), arch)
		}
		e.note("This server has glibc %s, too old for official Node.js 18+ builds. Using the glibc 2.17 build from unofficial-builds.nodejs.org.", glibc.MajorMinor())
		sources = []nodeSource{legacy}
	}

	var lastErr error
	for i, source := range sources {
		release, dest, err := fetchNode(e, source, want)
		if err != nil {
			return err
		}
		out, err := e.Exec(filepath.Join(dest, "bin", "node"), "-v")
		if err == nil {
			return linkNode(e, release, dest)
		}
		os.RemoveAll(dest)
		lastErr = fmt.Errorf("Node.js %s (%s) does not run on this server: %s", release.Version, source.flavor, firstLine(out, err))
		if i+1 < len(sources) {
			e.note("%v. Trying the glibc 2.17 build, which needs fewer system libraries.", lastErr)
		}
	}
	return lastErr
}

func fetchNode(e *Env, source nodeSource, want string) (nodeRelease, string, error) {
	releases, err := loadReleases(e, source.base)
	if err != nil {
		return nodeRelease{}, "", err
	}
	release, err := chooseNode(releases, want, source.flavor)
	if err != nil {
		return release, "", err
	}
	name := fmt.Sprintf("node-%s-%s.tar.gz", release.Version, source.flavor)
	dest := filepath.Join(e.dataDir(), "node", release.Version+"-"+source.flavor)
	if exists(filepath.Join(dest, "bin", "node")) {
		return release, dest, nil
	}
	e.say("Downloading Node.js %s (%s)...", release.Version, source.flavor)
	sums, err := fetch(e, source.base+"/"+release.Version+"/SHASUMS256.txt")
	if err != nil {
		return release, "", err
	}
	sum := checksumFor(sums, name)
	if sum == "" {
		return release, "", fmt.Errorf("%s is not listed in SHASUMS256.txt", name)
	}
	archive := filepath.Join(e.dataDir(), "node", name)
	if err := downloadVerified(e, source.base+"/"+release.Version+"/"+name, sum, archive); err != nil {
		return release, "", err
	}
	defer os.Remove(archive)
	tmp := dest + ".partial"
	os.RemoveAll(tmp)
	if err := extractTarGz(archive, tmp); err != nil {
		os.RemoveAll(tmp)
		return release, "", err
	}
	return release, dest, os.Rename(tmp, dest)
}

func linkNode(e *Env, release nodeRelease, dest string) error {
	current := filepath.Join(e.dataDir(), "node", "current")
	if err := replaceSymlink(dest, current); err != nil {
		return err
	}
	for _, tool := range []string{"node", "npm", "npx", "corepack"} {
		link := filepath.Join(e.binDir(), tool)
		if exists(filepath.Join(dest, "bin", tool)) {
			if err := replaceSymlink(filepath.Join(current, "bin", tool), link); err != nil {
				return err
			}
		} else if target, err := os.Readlink(link); err == nil && strings.HasPrefix(target, current) {
			os.Remove(link)
		}
	}
	if err := setupShell(e, true); err != nil {
		return err
	}
	e.done("Node.js %s is installed at %s", release.Version, tildePath(e, filepath.Join(e.binDir(), "node")))
	return nil
}
