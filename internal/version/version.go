// Package version parses and gates the Prism Central and AOS versions this tool
// is qualified against.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// Tool is the build version of this application, overridable at link time.
var Tool = "0.1.0-dev"

// SupportedMajor and SupportedMinor are the only Prism Central and AOS release
// this iteration is qualified for. Everything else needs the older-API pipeline
// tracked as future work.
const (
	SupportedMajor = 7
	SupportedMinor = 5
)

// versionPattern pulls the first "major.minor" pair out of strings such as
// "pc.7.5", "pc.2024.3.1", "7.5", "7.5.0.1" or "master".
var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// Parse interprets a version string reported by a Prism API.
func Parse(product, raw, source string) model.ProductVersion {
	v := model.ProductVersion{
		Product: product,
		Raw:     strings.TrimSpace(raw),
		Source:  source,
	}
	if m := versionPattern.FindStringSubmatch(v.Raw); m != nil {
		v.Major, _ = strconv.Atoi(m[1])
		v.Minor, _ = strconv.Atoi(m[2])
	}
	return v
}

// IsSupported reports whether a parsed version is the qualified release.
func IsSupported(v model.ProductVersion) bool {
	return v.Major == SupportedMajor && v.Minor == SupportedMinor
}

// Requirement renders the supported release for error and log messages.
func Requirement() string {
	return fmt.Sprintf("%d.%d", SupportedMajor, SupportedMinor)
}
