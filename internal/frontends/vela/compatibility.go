package vela

import (
	"regexp"
	"strings"
)

// The supported interval is >=0.1.0 <0.3.0: 0.1.x is the web client and
// 0.2.x the React Native client, both on /vela/v0. Validate full semver syntax before
// comparing its relevant components; build metadata never changes precedence.
var buildVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func compatibleBuild(version string) bool {
	if len(version) > 256 {
		return false
	}
	parts := buildVersion.FindStringSubmatch(version)
	if parts == nil {
		return false
	}
	prerelease := parts[4]
	if prerelease != "" {
		for _, id := range strings.Split(prerelease, ".") {
			if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
				return false
			}
		}
	}
	if parts[1] != "0" {
		return false
	}
	switch parts[2] {
	case "1":
		return parts[3] != "0" || prerelease == ""
	case "2":
		return true
	}
	return parts[2] == "3" && parts[3] == "0" && prerelease != ""
}
