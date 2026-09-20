# npm packaging

`llmsh` is a Go binary. npm is one of the ways people get it.

The layout is esbuild's: a wrapper package that depends on one package per
platform, each carrying a single binary and declaring `os` and `cpu`. npm
resolves those fields itself and downloads only the one that matches, so there
is no `postinstall` script.

That absence is the point. A `postinstall` that fetches a binary from the
internet runs arbitrary code at install time, is blocked outright in hardened
CI, and is the exact shape this project's own scanner flags in a skill. Asking
people to trust it while telling them we check for it would be difficult to
defend.

`publish.sh` builds the platform packages from a release's binaries and
publishes them. It is run by hand, because publishing to a registry under your
own name is a thing to do deliberately.
