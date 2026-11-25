# hostfix

[![CI](https://github.com/rmiyoyo/hostfix/actions/workflows/ci.yml/badge.svg)](https://github.com/rmiyoyo/hostfix/actions/workflows/ci.yml)

**Fix PHP, Composer, Node.js and permission problems on cPanel and CloudLinux shared hosting, over SSH, without root.**

Shared hosting gives you a website PHP, a different command line PHP, an old Composer, an older Node.js (or none), and file permissions that turn into 500 errors. hostfix finds these problems and fixes them in your own account. It is one static binary with no dependencies.

```sh
curl -fsSL https://github.com/rmiyoyo/hostfix/releases/latest/download/install.sh | sh
source ~/.bashrc
cd ~/my-laravel-app
hostfix doctor
hostfix fix
```

## What it fixes

| You see | Cause | hostfix |
| --- | --- | --- |
| `Composer detected issues in your platform: Your Composer dependencies require a PHP version ">= 8.1.0". You are running 7.4.33.` | SSH uses the server's default PHP, not your site's | `hostfix php use auto` reads `composer.json` and `composer.lock` and picks the newest installed PHP that meets every version and extension requirement |
| The site works but `php artisan` or `composer` fails, or the other way round | Website PHP (MultiPHP Manager) and command line PHP differ | `hostfix doctor` shows both; `hostfix php use <version>` or `hostfix php web <version>` makes them match |
| `the requested PHP extension intl is missing from your system` | Extension not enabled for that PHP version | `doctor` lists which installed versions have it |
| `Allowed memory size of 134217728 bytes exhausted` during `composer install` | Low `memory_limit` | The hostfix Composer wrapper runs with `COMPOSER_MEMORY_LIMIT=-1` |
| Composer 1.x, or `composer: command not found` | Old or missing cPanel Composer | `hostfix composer` installs the latest Composer, checksum verified |
| `npm WARN EBADENGINE Unsupported engine ... current: {"node":"v16.20.2"}` | Old or no Node.js | `hostfix node install auto` reads `engines.node` from `package.json` and installs a matching release from nodejs.org, checksum verified |
| `node: /lib64/libc.so.6: version 'GLIBC_2.28' not found` | CentOS 7 or CloudLinux 7 has glibc 2.17; Node.js 18+ needs 2.28 | Switches to the glibc 2.17 build from unofficial-builds.nodejs.org automatically |
| `error while loading shared libraries: libatomic.so.1` | Node.js 24+ needs a library the server lacks | Tests the build before switching and falls back to the glibc 2.17 build, which runs without it |
| `sh: node_modules/.bin/vite: Permission denied` | Executable bit lost on upload | `hostfix perms` |
| 500 Internal Server Error after upload, "is writable by others" in the suEXEC log | suEXEC and suPHP refuse group or world writable files | `hostfix perms` removes group and world write, and nothing else |
| `npm ERR! code EACCES` on the npm cache | Cache owned by another user after a migration | `hostfix fix` moves the cache to `~/.npm-cache` |
| Uploaded images return 404 in Laravel | `public/storage` link missing, and `symlink()` is disabled so `storage:link` fails | `hostfix laravel storage-link` makes the link without PHP |
| Laravel site shows a directory listing or 403 | `public_html` is not the `public/` folder | `hostfix laravel docroot --yes ~/app` points `public_html` at it and keeps the old folder |
| Cron runs the wrong PHP | Cron does not read `~/.bashrc` | `hostfix cron` prints a cron line with the full path to your PHP |
| `failed to open stream: /Users/you/app/storage/...` | `config:cache` was run on your computer and uploaded | `doctor` spots it and tells you to run `php artisan config:clear` |

`hostfix doctor` also warns about disabled PHP functions (`proc_open`, `symlink`, `putenv` and others) and what each one breaks, `APP_DEBUG=true` in production, a missing `APP_KEY`, and memory or process limits that kill `npm run build`.

## Commands

```
hostfix doctor [dir]                     Check the server and a project for common problems
hostfix fix [dir]                        Fix everything doctor can fix safely, then check again

hostfix php list                         List installed PHP versions
hostfix php use <version|auto> [dir]     Set the command line PHP
hostfix php web <version> [dir]          Set the PHP version a site runs on (.htaccess)
hostfix composer                         Install the latest Composer
hostfix node install <version|lts|auto> [dir]
                                         Install Node.js without root
hostfix perms [--dry-run] [dir]          Fix permissions behind 500 errors and "Permission denied"
hostfix laravel storage-link [dir]       Link public/storage without PHP's symlink()
hostfix laravel docroot [--yes] <dir>    Point ~/public_html at a Laravel public/ folder
hostfix cron [dir]                       Print a cron line that runs the right PHP
hostfix shell                            Put ~/.local/bin first in PATH for SSH sessions
hostfix version
```

`dir` defaults to the current folder. `doctor` exits with 1 when it finds a problem, so it can run in deploy scripts.

## Example

```
$ hostfix doctor
hostfix 3.0.0 checking ~/shop

PHP
  ✓ Installed versions: 7.4, 8.1, 8.2, 8.3
  ✓ Command line PHP 7.4.33 (/usr/local/bin/php)
  ! The website runs PHP 8.2 (~/public_html/.htaccess) but the command line runs 7.4.
    fix: hostfix php use 8.2   or   hostfix php web 7.4

Composer
  ✓ Composer 2.8.4
  ✗ PHP 7.4.33 does not meet this project's requirements:
      composer.json needs PHP ^8.1
    fix: hostfix php use auto   (picks PHP 8.3)

Node.js
  ✗ node is not in PATH
    fix: hostfix node install auto

Files
  ✗ 37 paths: group or world writable (suEXEC and suPHP refuse these with a 500 error)
    fix: hostfix perms

2 problems and 1 warning. Run `hostfix fix` to fix what can be fixed automatically.
```

## How it works

- Everything lives in your home folder: `~/.local/bin` for `php`, `composer`, `node`, `npm` and `npx`, and `~/.local/share/hostfix` for downloads. Nothing needs root.
- `~/.bashrc` gets one marked block that puts `~/.local/bin` first in `PATH`. Running hostfix again never adds a second copy. If your login shell file does not load `~/.bashrc`, hostfix adds one line so it does.
- PHP versions are found under `/opt/cpanel/ea-php*` (EasyApache 4) and `/opt/alt/php*` (CloudLinux). The website version comes from the cPanel handler in `.htaccess`, or the server default in `/etc/cpanel/ea4/ea4.conf`.
- Node.js and Composer downloads are checked against their published SHA-256 checksums. A new Node.js is run once before hostfix switches to it, so a build that cannot start never replaces a working one.
- `perms` only takes away group and world write and adds missing owner bits. It never makes anything more open, never follows symlinks, and leaves files like a `600` `.env` alone.
- Any file hostfix rewrites (`~/.bashrc`, `.htaccess`) is backed up first as `<file>.hostfix-<date>`. `laravel docroot` renames the old `public_html` and never deletes it.

## Install

```sh
curl -fsSL https://github.com/rmiyoyo/hostfix/releases/latest/download/install.sh | sh
```

This downloads the binary for your CPU (x86_64 or arm64), checks it against its checksum, saves it to `~/.local/bin/hostfix`, and sets up your `PATH`. To install a specific version, set `HOSTFIX_VERSION=v3.0.0` first.

Or download `hostfix-linux-amd64` from the [releases page](https://github.com/rmiyoyo/hostfix/releases) yourself, upload it, and run `chmod +x hostfix && ./hostfix shell`.

To build from source: `go build -o hostfix .` (Go 1.26+).

## Upgrading from server-env-setup.sh

hostfix replaces `server-env-setup.sh` from the old PHP-Version-Management repository. On first run it removes the old `~/.bashrc` block, the `php` and `composer` aliases, and the `~/perl5/bin/php` symlink. Your existing `~/.local/bin/php` choice keeps working. nvm installs are still loaded if you have one, but `~/.local/bin` comes first.

## Development

```sh
go test ./...
go vet ./...
```

The tests build a fake cPanel server in a temporary folder (PHP binaries under `/opt`, a home folder, `.htaccess` files) and a local download server, so they cover real file changes and downloads without touching your system. Releases are built by pushing a `v*` tag.

## License

MIT
