package install

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionRE is the accepted form of a release version: major.minor.patch with an optional pre-release. Build
// metadata is refused so a version is always a safe directory name.
var versionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z][0-9A-Za-z.-]*))?$`)

// NormalizeVersion strips a leading "v" and validates the rest ("v0.2.0" -> "0.2.0").
func NormalizeVersion(v string) (string, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !versionRE.MatchString(v) {
		return "", fmt.Errorf("install: invalid version %q", v)
	}
	return v, nil
}

// CompareVersions returns -1, 0 or 1 following semver precedence (a pre-release is lower than its release).
func CompareVersions(a, b string) (int, error) {
	am, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bm, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range 3 {
		if c := cmpUint(am.core[i], bm.core[i]); c != 0 {
			return c, nil
		}
	}
	switch {
	case am.pre == "" && bm.pre == "":
		return 0, nil
	case am.pre == "":
		return 1, nil
	case bm.pre == "":
		return -1, nil
	}
	return comparePre(am.pre, bm.pre), nil
}

type parsedVersion struct {
	core [3]uint64
	pre  string
}

func parseVersion(v string) (parsedVersion, error) {
	n, err := NormalizeVersion(v)
	if err != nil {
		return parsedVersion{}, err
	}
	m := versionRE.FindStringSubmatch(n)
	var p parsedVersion
	for i := range 3 {
		x, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return parsedVersion{}, fmt.Errorf("install: invalid version %q", n)
		}
		p.core[i] = x
	}
	p.pre = m[4]
	return p, nil
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.ParseUint(as[i], 10, 64)
		bn, berr := strconv.ParseUint(bs[i], 10, 64)
		switch {
		case aerr == nil && berr == nil:
			if c := cmpUint(an, bn); c != 0 {
				return c
			}
		case aerr == nil: // numeric identifiers sort below alphanumeric ones
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpUint(uint64(len(as)), uint64(len(bs)))
}

// isDevVersion reports a build without a release version ("", "dev", "(devel)", a git describe): it passes every
// minHubVersion check.
func isDevVersion(v string) bool {
	_, err := NormalizeVersion(v)
	return err != nil
}
