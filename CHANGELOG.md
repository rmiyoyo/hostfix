# Changelog

## 3.0.0

The project is now **hostfix**, a single Go binary, replacing `server-env-setup.sh`.

### Added
- `hostfix doctor`: checks shell PATH, command line and website PHP, Composer requirements (PHP version and extensions from `composer.json` and `composer.lock`), disabled PHP functions, Composer version, Node.js against `engines.node`, glibc problems, npm cache, memory and process limits, file permissions and Laravel setup
- `hostfix fix`: applies every safe fix, then runs `doctor` again
- `hostfix php use auto`: picks the newest installed PHP that meets the project's requirements
- `hostfix php web`: sets the site PHP version with the cPanel `.htaccess` handler
- `hostfix composer`: installs the latest Composer with checksum verification and no memory limit
- `hostfix node install`: installs Node.js from nodejs.org without nvm, verified by checksum, with automatic glibc 2.17 builds for CentOS 7 and CloudLinux 7, and a fallback when the official build cannot start (for example missing `libatomic.so.1`)
- `hostfix perms`: removes group and world write that cause suEXEC 500 errors, and makes `node_modules/.bin` tools executable
- `hostfix laravel storage-link` and `hostfix laravel docroot`
- `hostfix cron`: cron lines with the full PHP path
- `install.sh`, release builds for x86_64 and arm64, and tests that run against a fake cPanel server

### Changed
- Login shells now load `~/.bashrc` when they did not before
- The old `server-env-setup` block, aliases and `~/perl5/bin/php` symlink are cleaned up on first run

### Removed
- `server-env-setup.sh` and its interactive menu

## 2.0.0

- Command-line options, detection of installed PHP versions, a Composer wrapper, any Node.js major version, nested `node_modules/.bin` and Laravel permission fixes, `~/.bashrc` backups and a ShellCheck workflow

## 1.0.0

- Initial release: interactive PHP and Node.js setup for shared hosting
