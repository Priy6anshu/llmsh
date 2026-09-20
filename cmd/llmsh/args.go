package main

import "flag"

// parseArgs parses flags and positionals in any order.
//
// Go's flag package stops at the first non-flag argument, so `llmsh publish ./dir
// --dry-run` would silently ignore the flag while `llmsh publish --dry-run ./dir`
// worked. Splitting them by hand instead -- taking the first argument that does
// not start with a dash as the positional -- is worse still: in `--name csv-tidy`
// that argument is the flag's VALUE, so the name was dropped and used as the
// directory, which is exactly what happened here.
//
// Parsing repeatedly and peeling off one positional each time is the standard
// answer, and it is the flag package doing the deciding rather than a guess
// about which tokens are values.
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return positional
		}
		if fs.NArg() == 0 {
			return positional
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// first returns the first positional, or def.
func first(positional []string, def string) string {
	if len(positional) > 0 && positional[0] != "" {
		return positional[0]
	}
	return def
}
