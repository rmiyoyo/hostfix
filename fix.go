package main

import (
	"os"
	"path/filepath"
)

func fix(e *Env, dir string) int {
	e.say("hostfix %s fixing %s\n", version, tildePath(e, dir))
	step := func(err error) {
		if err != nil {
			e.note("%v", err)
		}
	}

	step(setupShell(e, false))
	e.pathFirst(e.binDir())

	req, err := readPHPRequirements(dir)
	step(err)
	if len(discoverPHP(e)) > 0 && needsPHPSwitch(e, req) {
		_, err := phpUse(e, "auto", dir)
		step(err)
	}

	if composer := e.lookPath("composer"); req != nil {
		if v, ok := composerVersion(e, composer); composer == "" || !ok || v[0] < 2 {
			step(composerInstall(e))
		}
	}

	project, err := readNodeProject(dir)
	step(err)
	if project != nil && needsNode(e, project) {
		step(nodeInstall(e, "auto", dir))
	}

	if cache := npmCache(e); cache != "" && !writable(cache) {
		newCache := filepath.Join(e.Home, ".npm-cache")
		if out, err := e.Exec(e.lookPath("npm"), "config", "set", "cache", newCache); err != nil {
			e.note("could not move the npm cache: %s", firstLine(out, err))
		} else {
			os.MkdirAll(newCache, 0o755)
			e.done("Moved the npm cache to ~/.npm-cache")
		}
	}

	if dir != e.Home {
		step(fixPerms(e, dir, false))
	}
	if storageLinkMissing(dir) {
		step(storageLink(e, dir))
	}

	e.say("")
	return doctor(e, dir)
}

func needsPHPSwitch(e *Env, req *PHPRequirements) bool {
	php := e.lookPath("php")
	if php == "" {
		return true
	}
	info, err := probePHP(e, php)
	if err != nil {
		return true
	}
	return req != nil && len(req.problems(info)) > 0
}

func needsNode(e *Env, project *nodeProject) bool {
	node := e.lookPath("node")
	if node == "" {
		return true
	}
	out, err := e.Exec(node, "-v")
	if err != nil {
		return true
	}
	v, _ := parseVersion(out)
	return project.Engine != "" && !satisfiesNPM(project.Engine, v)
}
