package main

import (
	"regexp"
	"strconv"
	"strings"
)

type Version [3]int

func (v Version) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}

func (v Version) MajorMinor() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1])
}

func compareVersions(a, b Version) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

var versionPrefix = regexp.MustCompile(`^v?(\d+)(?:\.(\d+|[x*]))?(?:\.(\d+|[x*]))?`)

func parsePartial(s string) (Version, int, bool) {
	m := versionPrefix.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, 0, false
	}
	var v Version
	parts := 0
	for i := 1; i <= 3; i++ {
		if m[i] == "" || m[i] == "x" || m[i] == "*" {
			break
		}
		v[i-1], _ = strconv.Atoi(m[i])
		parts++
	}
	return v, parts, true
}

func parseVersion(s string) (Version, bool) {
	v, _, ok := parsePartial(s)
	return v, ok
}

var (
	orSplit         = regexp.MustCompile(`\s*\|\|?\s*`)
	hyphenRange     = regexp.MustCompile(`(\S+)\s+-\s+(\S+)`)
	operatorSpace   = regexp.MustCompile(`(>=|<=|!=|==|>|<|=|\^|~)\s+`)
	andSplit        = regexp.MustCompile(`[\s,]+`)
	stabilitySuffix = regexp.MustCompile(`@\w+$`)
)

func satisfies(constraint string, v Version) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	for _, alternative := range orSplit.Split(constraint, -1) {
		if satisfiesAll(alternative, v) {
			return true
		}
	}
	return false
}

func satisfiesAll(group string, v Version) bool {
	group = hyphenRange.ReplaceAllString(group, ">=$1 <=$2")
	group = operatorSpace.ReplaceAllString(group, "$1")
	for _, token := range andSplit.Split(strings.TrimSpace(group), -1) {
		if token != "" && !satisfiesOne(stabilitySuffix.ReplaceAllString(token, ""), v) {
			return false
		}
	}
	return true
}

func satisfiesOne(token string, v Version) bool {
	if token == "*" || token == "x" || token == "" {
		return true
	}
	op := ""
	for _, candidate := range []string{">=", "<=", "!=", "==", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, candidate) {
			op = candidate
			break
		}
	}
	target, parts, ok := parsePartial(strings.TrimPrefix(token, op))
	if !ok {
		return false
	}
	cmp := compareVersions(v, target)
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		if parts < 3 {
			return compareVersions(v, bump(target, parts)) < 0
		}
		return cmp <= 0
	case "<":
		return cmp < 0
	case "!=":
		return !matchesPrefix(v, target, parts)
	case "^":
		return cmp >= 0 && compareVersions(v, caretLimit(target, parts)) < 0
	case "~":
		limitParts := parts - 1
		if limitParts < 1 {
			limitParts = 1
		}
		return cmp >= 0 && compareVersions(v, bump(target, limitParts)) < 0
	default:
		return matchesPrefix(v, target, parts)
	}
}

func matchesPrefix(v, target Version, parts int) bool {
	for i := 0; i < parts; i++ {
		if v[i] != target[i] {
			return false
		}
	}
	return true
}

func bump(v Version, parts int) Version {
	var out Version
	copy(out[:parts], v[:parts])
	out[parts-1]++
	return out
}

func caretLimit(v Version, parts int) Version {
	switch {
	case v[0] > 0 || parts == 1:
		return bump(v, 1)
	case v[1] > 0 || parts == 2:
		return bump(v, 2)
	default:
		return bump(v, 3)
	}
}
