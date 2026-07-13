package buildinfo

import (
	"fmt"
	"runtime"
)

// These values are replaced with -ldflags at release build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Name      string
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	OS        string
	Arch      string
}

func Current(name string) Info {
	return Info{
		Name:      name,
		Version:   Version,
		Commit:    Commit,
		BuildDate: Date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit %s, built %s, %s, %s/%s)", i.Name, i.Version, i.Commit, i.BuildDate, i.GoVersion, i.OS, i.Arch)
}
