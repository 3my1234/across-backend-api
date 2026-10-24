package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Revision is injected by image builds. It never reads runtime credentials.
var Revision = "unknown"

func Snapshot(service string) map[string]any {
	revision := Revision
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && revision == "unknown" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" {
				modified = setting.Value == "true"
			}
		}
	}
	return map[string]any{"service": service, "revision": revision, "modified": modified, "go": runtime.Version()}
}
