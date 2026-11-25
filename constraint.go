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

var versionPrefix = regexp.MustCompile(`^v?(\d+)(?:\.(\d+|[xX*]))?(?:\.(\d+|[xX*]))?`)

func parsePartial(s string) (Version, int, bool, bool) {
	m := versionPrefix.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, 0, false, false
	}
	var v Version
	parts := 0
	wildcard := false
	for i := 1; i <= 3; i++ {
		if m[i] == "" {
			break
		}
		if m[i] == "x" || m[i] == "X" || m[i] == "*" {
			wildcard = true
			break
		}
		v[i-1], _ = strconv.Atoi(m[i])
		parts++
	}
	return v, parts, wildcard, true
}

func parseVersion(s string) (Version, bool) {
	v, _, _, ok := parsePartial(s)
	return v, ok
}

type constraintStyle int

const (
	composerStyle constraintStyle = iota
	npmStyle
)

var (
	orSplit         = regexp.MustCompile(`\s*\|\|?\s*`)
	hyphenRange     = regexp.MustCompile(`(\S+)\s+-\s+(\S+)`)
	operatorSpace   = regexp.MustCompile(`(>=|<=|!=|<>|==|>|<|=|\^|~)\s+`)
	andSplit        = regexp.MustCompile(`[\s,]+`)
	stabilitySuffix = regexp.MustCompile(`@\w+$`)
	anyVersion      = regexp.MustCompile(`^[xX*](\.[xX*])*$`)
)

func satisfies(constraint string, v Version) bool {
	return satisfiesStyle(constraint, v, composerStyle)
}

func satisfiesNPM(constraint string, v Version) bool {
	return satisfiesStyle(constraint, v, npmStyle)
}

func satisfiesStyle(constraint string, v Version, style constraintStyle) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	for _, alternative := range orSplit.Split(constraint, -1) {
		if satisfiesAll(alternative, v, style) {
			return true
		}
	}
	return false
}

func satisfiesAll(group string, v Version, style constraintStyle) bool {
	group = hyphenRange.ReplaceAllString(group, ">=$1 <=*$2")
	group = operatorSpace.ReplaceAllString(group, "$1")
	for _, token := range andSplit.Split(strings.TrimSpace(group), -1) {
		if token != "" && !satisfiesOne(stabilitySuffix.ReplaceAllString(token, ""), v, style) {
			return false
		}
	}
	return true
}

func satisfiesOne(token string, v Version, style constraintStyle) bool {
	if token == "" || anyVersion.MatchString(token) {
		return true
	}
	op := ""
	for _, candidate := range []string{"<=*", ">=", "<=", "!=", "<>", "==", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, candidate) {
			op = candidate
			break
		}
	}
	target, parts, wildcard, ok := parsePartial(strings.TrimPrefix(token, op))
	if !ok {
		return false
	}
	if parts == 0 {
		return op == "" || op == "=" || op == "==" || op == ">="
	}
	partial := parts < 3 && (style == npmStyle || wildcard)
	cmp := compareVersions(v, target)
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		if partial {
			return compareVersions(v, bump(target, parts)) >= 0
		}
		return cmp > 0
	case "<=*":
		return compareVersions(v, bump(target, parts)) < 0
	case "<=":
		if partial {
			return compareVersions(v, bump(target, parts)) < 0
		}
		return cmp <= 0
	case "<":
		return cmp < 0
	case "!=", "<>":
		if partial {
			return !matchesPrefix(v, target, parts)
		}
		return cmp != 0
	case "^":
		return cmp >= 0 && compareVersions(v, caretLimit(target, parts)) < 0
	case "~":
		limit := parts - 1
		if style == npmStyle {
			limit = min(parts, 2)
		}
		return cmp >= 0 && compareVersions(v, bump(target, max(limit, 1))) < 0
	default:
		if partial {
			return matchesPrefix(v, target, parts)
		}
		return cmp == 0
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
