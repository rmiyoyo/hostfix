package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeServer struct {
	t      *testing.T
	env    *Env
	out    *bytes.Buffer
	byPath map[string]string
	byCmd  map[string]string
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	home := filepath.Join(root, "home", "user")
	must(t, os.MkdirAll(home, 0o755))
	out := &bytes.Buffer{}
	f := &fakeServer{t: t, out: out, byPath: map[string]string{}, byCmd: map[string]string{}}
	f.env = &Env{
		Root: root,
		Home: home,
		Path: filepath.Join(home, ".local", "bin"),
		Out:  out,
		Err:  out,
		Exec: f.exec,
		HTTP: http.DefaultClient,
		Now:  func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) },
	}
	return f
}

func (f *fakeServer) exec(name string, args ...string) (string, error) {
	if real, err := filepath.EvalSymlinks(name); err == nil {
		if out, ok := f.byPath[real]; ok {
			return out, nil
		}
	}
	if out, ok := f.byCmd[filepath.Base(name)+" "+strings.Join(args, " ")]; ok {
		return out, nil
	}
	return "", errors.New("not found")
}

func (f *fakeServer) addPHP(version, source, extensions, disabled string) string {
	digits := strings.ReplaceAll(version, ".", "")
	path := filepath.Join(f.env.Root, "opt", "cpanel", "ea-php"+digits, "root", "usr", "bin", "php")
	if source == "alt-php" {
		path = filepath.Join(f.env.Root, "opt", "alt", "php"+digits, "usr", "bin", "php")
	}
	f.writeExec(path)
	f.byPath[path] = probeMarker + "\n" + fmt.Sprintf("%s.4\n%s\n%s\n128M\n", version, extensions, disabled)
	return path
}

func (f *fakeServer) writeExec(path string) {
	must(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(f.t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
}

func (f *fakeServer) write(rel, content string, mode os.FileMode) string {
	path := filepath.Join(f.env.Home, rel)
	must(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(f.t, os.WriteFile(path, []byte(content), mode))
	must(f.t, os.Chmod(path, mode))
	return path
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	return string(data)
}

func TestDiscoverPHP(t *testing.T) {
	f := newFakeServer(t)
	f.addPHP("8.3", "ea-php", "", "")
	f.addPHP("8.1", "ea-php", "", "")
	f.addPHP("7.4", "alt-php", "", "")
	f.addPHP("8.3", "alt-php", "", "")
	got := discoverPHP(f.env)
	var summary []string
	for _, b := range got {
		summary = append(summary, b.Version+" "+b.Source)
	}
	if want := "7.4 alt-php,8.1 ea-php,8.3 ea-php,8.3 alt-php"; strings.Join(summary, ",") != want {
		t.Fatalf("got %v, want %s", summary, want)
	}
}

func TestPHPUseAutoPicksNewestVersionMeetingComposerLock(t *testing.T) {
	f := newFakeServer(t)
	f.addPHP("8.1", "ea-php", "Core,intl,PDO", "")
	php83 := f.addPHP("8.3", "ea-php", "Core,intl,pdo_mysql", "")
	f.addPHP("8.4", "ea-php", "Core,pdo_mysql", "")
	f.write("app/composer.json", `{"require":{"php":"^8.1","ext-intl":"*"}}`, 0o644)
	f.write("app/composer.lock", `{"packages":[{"name":"symfony/console","require":{"php":">=8.2","ext-pdo_mysql":"*"}}]}`, 0o644)

	chosen, err := phpUse(f.env, "auto", filepath.Join(f.env.Home, "app"))
	must(t, err)
	if chosen.Version != "8.3" {
		t.Fatalf("picked %s, want 8.3", chosen.Version)
	}
	target, err := filepath.EvalSymlinks(filepath.Join(f.env.binDir(), "php"))
	must(t, err)
	if target != php83 {
		t.Fatalf("~/.local/bin/php -> %s, want %s", target, php83)
	}
	if !strings.Contains(readFile(t, filepath.Join(f.env.Home, ".bashrc")), blockBegin) {
		t.Fatal("PATH block not written to ~/.bashrc")
	}
}

func TestPHPUseAutoExplainsWhenNothingFits(t *testing.T) {
	f := newFakeServer(t)
	f.addPHP("7.4", "ea-php", "Core", "")
	f.write("app/composer.json", `{"require":{"php":">=8.0.2"}}`, 0o644)
	_, err := phpUse(f.env, "auto", filepath.Join(f.env.Home, "app"))
	if err == nil || !strings.Contains(err.Error(), "7.4: composer.json needs PHP >=8.0.2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShellMigratesOldScriptAndIsIdempotent(t *testing.T) {
	f := newFakeServer(t)
	bashrc := f.write(".bashrc", strings.Join([]string{
		"export EDITOR=nano",
		"alias php='/opt/cpanel/ea-php74/root/usr/bin/php'",
		oldBlockBegin,
		`export PATH="$HOME/.local/bin:$PATH"`,
		oldBlockEnd,
		"alias ll='ls -l'",
		"",
	}, "\n"), 0o644)

	must(t, setupShell(f.env, false))
	first := readFile(t, bashrc)
	must(t, setupShell(f.env, false))
	second := readFile(t, bashrc)

	if first != second {
		t.Fatalf("second run changed ~/.bashrc:\n%s\n---\n%s", first, second)
	}
	for _, gone := range []string{oldBlockBegin, "ea-php74"} {
		if strings.Contains(second, gone) {
			t.Errorf("~/.bashrc still contains %q", gone)
		}
	}
	for _, kept := range []string{"export EDITOR=nano", "alias ll='ls -l'"} {
		if !strings.Contains(second, kept) {
			t.Errorf("~/.bashrc lost %q", kept)
		}
	}
	if strings.Count(second, blockBegin) != 1 {
		t.Errorf("expected one hostfix block:\n%s", second)
	}
	if !strings.Contains(readFile(t, filepath.Join(f.env.Home, ".bash_profile")), ".bashrc") {
		t.Error("~/.bash_profile does not load ~/.bashrc")
	}
	backups, _ := filepath.Glob(bashrc + ".hostfix-*")
	if len(backups) != 1 {
		t.Errorf("expected one backup, got %v", backups)
	}
}

func TestPHPWebReplacesCpanelHandler(t *testing.T) {
	f := newFakeServer(t)
	f.addPHP("8.3", "ea-php", "", "")
	must(t, os.MkdirAll(filepath.Join(f.env.Root, "opt", "cpanel", "ea-php81"), 0o755))
	htaccess := f.write("public_html/.htaccess", strings.Join([]string{
		"RewriteEngine On",
		"RewriteRule ^ index.php [L]",
		"",
		handlerBegin,
		"<IfModule mime_module>",
		"  AddHandler application/x-httpd-ea-php81 .php .php8 .phtml",
		"</IfModule>",
		handlerEnd,
		"",
	}, "\n"), 0o644)
	dir := filepath.Join(f.env.Home, "public_html")

	if v, _ := webPHP(f.env, dir); v != "8.1" {
		t.Fatalf("web PHP before = %s, want 8.1", v)
	}
	must(t, phpWeb(f.env, "8.3", dir))
	got := readFile(t, htaccess)
	if v, _ := webPHP(f.env, filepath.Join(dir, "sub", "folder")); v != "8.3" {
		t.Fatalf("web PHP after = %s, want 8.3\n%s", v, got)
	}
	if strings.Contains(got, "ea-php81") || !strings.Contains(got, "RewriteRule ^ index.php [L]") || strings.Count(got, handlerBegin) != 1 {
		t.Fatalf("unexpected .htaccess:\n%s", got)
	}
	if err := phpWeb(f.env, "7.2", dir); err == nil {
		t.Fatal("expected an error for a version that is not installed")
	}
}

func TestPermsFixesWritableFilesAndNodeBins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	f := newFakeServer(t)
	dir := filepath.Join(f.env.Home, "app")
	open := f.write("app/index.php", "<?php", 0o666)
	private := f.write("app/.env", "APP_KEY=", 0o600)
	tool := f.write("app/node_modules/vite/bin/vite.js", "#!/usr/bin/env node", 0o644)
	must(t, os.MkdirAll(filepath.Join(dir, "node_modules", ".bin"), 0o755))
	must(t, os.Symlink("../vite/bin/vite.js", filepath.Join(dir, "node_modules", ".bin", "vite")))
	must(t, os.Chmod(dir, 0o777))

	must(t, fixPerms(f.env, dir, false))

	for path, want := range map[string]os.FileMode{dir: 0o755, open: 0o644, private: 0o600, tool: 0o744} {
		info, err := os.Stat(path)
		must(t, err)
		if info.Mode().Perm() != want {
			t.Errorf("%s is %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	changes, err := planPerms(dir)
	must(t, err)
	if len(changes) != 0 {
		t.Errorf("second plan should be empty, got %v", changes)
	}
}

func TestStorageLinkAndCron(t *testing.T) {
	f := newFakeServer(t)
	dir := filepath.Join(f.env.Home, "app")
	f.write("app/artisan", "", 0o644)
	must(t, os.MkdirAll(filepath.Join(dir, "public"), 0o755))
	php := f.addPHP("8.3", "ea-php", "", "")

	must(t, storageLink(f.env, dir))
	f.write("app/storage/app/public/logo.png", "png", 0o644)
	if readFile(t, filepath.Join(dir, "public", "storage", "logo.png")) != "png" {
		t.Fatal("public/storage does not reach storage/app/public")
	}

	f.out.Reset()
	must(t, printCron(f.env, dir))
	if want := fmt.Sprintf("* * * * * cd %s && %s artisan schedule:run", shellQuote(dir), shellQuote(php)); !strings.Contains(f.out.String(), want) {
		t.Fatalf("cron output %q does not contain %q", f.out.String(), want)
	}
}

func TestDocrootNeedsYesAndKeepsOldFolder(t *testing.T) {
	f := newFakeServer(t)
	dir := filepath.Join(f.env.Home, "app")
	f.write("app/public/index.php", "<?php", 0o644)
	f.write("public_html/old.html", "old", 0o644)

	must(t, docroot(f.env, dir, false, false))
	if info, _ := os.Lstat(filepath.Join(f.env.Home, "public_html")); info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("changed public_html without --yes")
	}
	must(t, docroot(f.env, dir, true, false))
	if readFile(t, filepath.Join(f.env.Home, "public_html", "index.php")) != "<?php" {
		t.Fatal("public_html does not serve the Laravel public folder")
	}
	if readFile(t, filepath.Join(f.env.Home, "public_html.hostfix-20261125-120000", "old.html")) != "old" {
		t.Fatal("old public_html was not kept")
	}
}

func TestChooseNode(t *testing.T) {
	releases := []nodeRelease{
		{Version: "v27.0.0-alpha.1", LTS: false, Files: []string{"linux-x64"}},
		{Version: "v25.9.0", LTS: false, Files: []string{"linux-x64"}},
		{Version: "v24.22.3", LTS: "Krypton", Files: []string{"linux-x64", "linux-x64-glibc-217"}},
		{Version: "v22.23.5", LTS: "Jod", Files: []string{"linux-x64", "linux-x64-glibc-217"}},
		{Version: "v20.20.2", LTS: "Iron", Files: []string{"linux-x64"}},
		{Version: "v20.19.0", LTS: "Iron", Files: []string{"linux-x64"}},
	}
	cases := map[string]string{
		"lts":                            "v24.22.3",
		"":                               "v24.22.3",
		"20":                             "v20.20.2",
		"v22":                            "v22.23.5",
		"^18.0.0 || ^20.0.0 || >=22.0.0": "v24.22.3",
		"^20 || ^22":                     "v22.23.5",
		">=25":                           "v25.9.0",
	}
	for want, expected := range cases {
		got, err := chooseNode(releases, want, "linux-x64")
		if err != nil || got.Version != expected {
			t.Errorf("chooseNode(%q) = %s, %v; want %s", want, got.Version, err, expected)
		}
	}
	if got, _ := chooseNode(releases, "20", "linux-x64-glibc-217"); got.Version != "" {
		t.Errorf("picked %s without a glibc-217 build", got.Version)
	}
}

func nodeTarball(t *testing.T, version, flavor string) []byte {
	t.Helper()
	return nodeTarballRunning(t, version, flavor, "echo "+version)
}

func nodeTarballRunning(t *testing.T, version, flavor, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := fmt.Sprintf("node-%s-%s/", version, flavor)
	add := func(hdr *tar.Header, body string) {
		hdr.Size = int64(len(body))
		must(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(body))
		must(t, err)
	}
	add(&tar.Header{Name: top, Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: top + "bin/node", Typeflag: tar.TypeReg, Mode: 0o755}, "#!/bin/sh\n"+script+"\n")
	add(&tar.Header{Name: top + "lib/node_modules/npm/bin/npm-cli.js", Typeflag: tar.TypeReg, Mode: 0o755}, "npm")
	add(&tar.Header{Name: top + "bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js"}, "")
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func TestNodeInstallUsesGlibc217BuildOnOldServers(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOARCH != "amd64" {
		t.Skip("needs linux/amd64")
	}
	f := newFakeServer(t)
	f.byCmd["getconf GNU_LIBC_VERSION"] = "glibc 2.17"
	f.write("app/package.json", `{"engines":{"node":"^20 || ^22"},"scripts":{"build":"vite build"}}`, 0o644)

	official := nodeTarball(t, "v22.23.5", "linux-x64")
	legacy := nodeTarball(t, "v22.23.5", "linux-x64-glibc-217")
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	mux := http.NewServeMux()
	mux.HandleFunc("/dist/index.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"version":"v22.23.5","lts":"Jod","files":["linux-x64"]}]`)
	})
	mux.HandleFunc("/unofficial/index.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"version":"v22.23.5","lts":"Jod","files":["linux-x64","linux-x64-glibc-217"]}]`)
	})
	mux.HandleFunc("/unofficial/v22.23.5/SHASUMS256.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  node-v22.23.5-linux-x64-glibc-217.tar.gz\n%s  node-v22.23.5-linux-x64.tar.gz\n", sum(legacy), sum(official))
	})
	mux.HandleFunc("/unofficial/v22.23.5/node-v22.23.5-linux-x64-glibc-217.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(legacy)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f.env.NodeDist, f.env.NodeUnofficial = srv.URL+"/dist", srv.URL+"/unofficial"
	f.env.Exec = execCommandOr(f.exec)

	must(t, nodeInstall(f.env, "auto", filepath.Join(f.env.Home, "app")))

	out, err := execCommand(filepath.Join(f.env.binDir(), "node"), "-v")
	if err != nil || out != "v22.23.5" {
		t.Fatalf("node -v = %q, %v", out, err)
	}
	if readFile(t, filepath.Join(f.env.binDir(), "npm")) != "npm" {
		t.Fatal("npm link is broken")
	}
	if !strings.Contains(f.out.String(), "glibc 2.17") {
		t.Fatalf("expected a glibc note, got:\n%s", f.out.String())
	}
}

func execCommandOr(fallback func(string, ...string) (string, error)) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		if out, err := fallback(name, args...); err == nil {
			return out, nil
		}
		return execCommand(name, args...)
	}
}

func TestNodeInstallFallsBackWhenOfficialBuildDoesNotRun(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOARCH != "amd64" {
		t.Skip("needs linux/amd64")
	}
	f := newFakeServer(t)
	f.byCmd["getconf GNU_LIBC_VERSION"] = "glibc 2.39"
	broken := nodeTarballRunning(t, "v24.22.3", "linux-x64", "echo 'node: error while loading shared libraries: libatomic.so.1: cannot open shared object file' >&2; exit 127")
	working := nodeTarball(t, "v24.22.3", "linux-x64-glibc-217")
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unofficial := strings.HasPrefix(r.URL.Path, "/unofficial/")
		switch {
		case strings.HasSuffix(r.URL.Path, "index.json"):
			fmt.Fprint(w, `[{"version":"v24.22.3","lts":"Krypton","files":["linux-x64","linux-x64-glibc-217"]}]`)
		case strings.HasSuffix(r.URL.Path, "SHASUMS256.txt") && unofficial:
			fmt.Fprintf(w, "%s  node-v24.22.3-linux-x64-glibc-217.tar.gz\n", sum(working))
		case strings.HasSuffix(r.URL.Path, "SHASUMS256.txt"):
			fmt.Fprintf(w, "%s  node-v24.22.3-linux-x64.tar.gz\n", sum(broken))
		case unofficial:
			w.Write(working)
		default:
			w.Write(broken)
		}
	}))
	defer srv.Close()
	f.env.NodeDist, f.env.NodeUnofficial = srv.URL+"/dist", srv.URL+"/unofficial"
	f.env.Exec = execCommandOr(f.exec)

	must(t, nodeInstall(f.env, "lts", f.env.Home))

	target, err := filepath.EvalSymlinks(filepath.Join(f.env.binDir(), "node"))
	must(t, err)
	if !strings.Contains(target, "glibc-217") {
		t.Fatalf("node links to %s, want the glibc-217 build", target)
	}
	if !strings.Contains(f.out.String(), "libatomic.so.1") {
		t.Fatalf("expected the libatomic reason in output:\n%s", f.out.String())
	}
	if exists(filepath.Join(f.env.dataDir(), "node", "v24.22.3-linux-x64")) {
		t.Fatal("broken build was left on disk")
	}
}

func TestNodeInstallRejectsBadChecksum(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOARCH != "amd64" {
		t.Skip("needs linux/amd64")
	}
	f := newFakeServer(t)
	f.byCmd["getconf GNU_LIBC_VERSION"] = "glibc 2.34"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "index.json"):
			fmt.Fprint(w, `[{"version":"v24.22.3","lts":"Krypton","files":["linux-x64"]}]`)
		case strings.HasSuffix(r.URL.Path, "SHASUMS256.txt"):
			fmt.Fprint(w, strings.Repeat("0", 64)+"  node-v24.22.3-linux-x64.tar.gz\n")
		default:
			w.Write(nodeTarball(t, "v24.22.3", "linux-x64"))
		}
	}))
	defer srv.Close()
	f.env.NodeDist = srv.URL
	err := nodeInstall(f.env, "lts", f.env.Home)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if exists(filepath.Join(f.env.binDir(), "node")) {
		t.Fatal("node was linked despite a bad checksum")
	}
}

func TestComposerInstallWritesWrapper(t *testing.T) {
	f := newFakeServer(t)
	phar := []byte("phar-bytes")
	h := sha256.Sum256(phar)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			fmt.Fprintf(w, "%s  composer.phar\n", hex.EncodeToString(h[:]))
			return
		}
		w.Write(phar)
	}))
	defer srv.Close()
	f.env.ComposerURL = srv.URL + "/composer.phar"

	must(t, composerInstall(f.env))
	wrapper := readFile(t, filepath.Join(f.env.binDir(), "composer"))
	for _, want := range []string{"COMPOSER_MEMORY_LIMIT", "$HOME/.local/bin/php", filepath.Join(f.env.dataDir(), "composer.phar")} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("wrapper missing %q:\n%s", want, wrapper)
		}
	}
	if readFile(t, filepath.Join(f.env.dataDir(), "composer.phar")) != "phar-bytes" {
		t.Fatal("phar not saved")
	}
}

func TestDoctorFindsProblemsAndFixRepairsThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	f := newFakeServer(t)
	php81 := f.addPHP("8.1", "ea-php", "Core,pdo_mysql", "proc_open,symlink")
	f.addPHP("8.3", "ea-php", "Core,pdo_mysql", "")
	must(t, replaceSymlink(php81, filepath.Join(f.env.binDir(), "php")))
	composer := filepath.Join(f.env.Root, "usr", "local", "bin", "composer")
	f.writeExec(composer)
	f.byPath[composer] = "Composer version 2.8.1 2025-01-01"
	f.env.Path += string(os.PathListSeparator) + filepath.Dir(composer)

	dir := filepath.Join(f.env.Home, "app")
	f.write("app/artisan", "", 0o644)
	f.write("app/composer.json", `{"require":{"php":"^8.2","ext-pdo_mysql":"*"}}`, 0o644)
	f.write("app/.env", "APP_ENV=production\nAPP_DEBUG=true\nAPP_KEY=\n", 0o600)
	f.write("app/public/index.php", "<?php", 0o666)
	f.write("app/bootstrap/cache/config.php", "<?php return ['view' => ['compiled' => '/Users/dev/app/storage/framework/views']];", 0o644)
	f.write("app/.htaccess", "AddHandler application/x-httpd-ea-php83 .php\n", 0o644)

	if code := doctor(f.env, dir); code != 1 {
		t.Fatalf("doctor exit %d, want 1\n%s", code, f.out)
	}
	report := f.out.String()
	for _, want := range []string{
		"The website runs PHP 8.3 (~/app/.htaccess) but the command line runs 8.1",
		"PHP 8.1.4 does not meet this project's requirements",
		"composer.json needs PHP ^8.2",
		"hostfix php use auto   (picks PHP 8.3)",
		"proc_open breaks Composer scripts",
		"group or world writable",
		"APP_KEY is empty",
		"APP_DEBUG=true in production",
		"public/storage is not linked",
		"cached on another machine",
		"~/.bashrc does not put ~/.local/bin first",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("doctor report is missing %q", want)
		}
	}

	f.out.Reset()
	fix(f.env, dir)
	after := f.out.String()
	_, final, _ := strings.Cut(after, "checking")
	for _, gone := range []string{"does not meet this project's requirements", "group or world writable", "public/storage is not linked", "~/.bashrc does not put"} {
		if strings.Contains(final, gone) {
			t.Errorf("still reported after fix: %q\n%s", gone, final)
		}
	}
	for _, still := range []string{"APP_KEY is empty", "cached on another machine"} {
		if !strings.Contains(final, still) {
			t.Errorf("fix should leave %q for the user", still)
		}
	}
	for _, want := range []string{"Command line PHP 8.3.4", "The website runs the same version"} {
		if !strings.Contains(final, want) {
			t.Errorf("missing %q after fix:\n%s", want, final)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	f := newFakeServer(t)
	if code := run(f.env, []string{"nope"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if code := run(f.env, []string{"version"}); code != 0 {
		t.Fatalf("version exit %d", code)
	}
}
