package updater

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch int
	pre                 []string
	ok                  bool
}

func parseSemver(v string) semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i] // build metadata is ignored
	}
	var s semver
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		s.pre = strings.Split(v[i+1:], ".")
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}
		}
		nums[i] = n
	}
	s.major, s.minor, s.patch, s.ok = nums[0], nums[1], nums[2], true
	return s
}

// compareVersions returns -1, 0 or 1. Unparseable versions sort first.
func compareVersions(a, b string) int {
	x, y := parseSemver(a), parseSemver(b)
	if !x.ok || !y.ok {
		switch {
		case x.ok == y.ok:
			return 0
		case !x.ok:
			return -1
		default:
			return 1
		}
	}
	for _, d := range []int{x.major - y.major, x.minor - y.minor, x.patch - y.patch} {
		if d != 0 {
			return sign(d)
		}
	}
	// A release outranks its pre-releases.
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0
	case len(x.pre) == 0:
		return 1
	case len(y.pre) == 0:
		return -1
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		a, b := x.pre[i], y.pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return sign(len(x.pre) - len(y.pre))
}

func sign(d int) int {
	switch {
	case d < 0:
		return -1
	case d > 0:
		return 1
	}
	return 0
}
