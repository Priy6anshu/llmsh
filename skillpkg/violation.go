package skillpkg

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityError Severity = "error" // blocks the publish
	SeverityWarn  Severity = "warn"  // surfaces in the UI and ranks the review queue
	SeverityInfo  Severity = "info"
)

// Violation is one finding. The shape is deliberately identical to what the API
// returns in a problem+json `violations[]` array, so the CLI and the web uploader
// render the same annotated output from the same data.
type Violation struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"` // path inside the package
	Line     int      `json:"line,omitempty"` // 1-indexed, 0 when unknown
	Message  string   `json:"message"`
	Hint     string   `json:"hint,omitempty"` // how to fix it
}

func (v Violation) String() string {
	loc := ""
	if v.Path != "" {
		loc = v.Path
		if v.Line > 0 {
			loc = fmt.Sprintf("%s:%d", v.Path, v.Line)
		}
		loc += ": "
	}
	return fmt.Sprintf("[%s] %s%s (%s)", v.Severity, loc, v.Message, v.Code)
}

// Result accumulates findings. A Result with no errors is publishable; warnings
// never block, by design — the moment a marketplace is stricter than the runtime,
// creators route around it.
type Result struct {
	Violations []Violation
}

func (r *Result) add(sev Severity, code, msg string, opts ...func(*Violation)) {
	v := Violation{Code: code, Severity: sev, Message: msg}
	for _, o := range opts {
		o(&v)
	}
	r.Violations = append(r.Violations, v)
}

func (r *Result) Errorf(code, format string, a ...any) {
	r.add(SeverityError, code, fmt.Sprintf(format, a...))
}

func (r *Result) Warnf(code, format string, a ...any) {
	r.add(SeverityWarn, code, fmt.Sprintf(format, a...))
}

// At attaches a location to the most recently added violation.
func At(path string, line int) func(*Violation) {
	return func(v *Violation) { v.Path, v.Line = path, line }
}

// Hint attaches a fix suggestion.
func Hint(h string) func(*Violation) { return func(v *Violation) { v.Hint = h } }

func (r *Result) Add(sev Severity, code, msg string, opts ...func(*Violation)) {
	r.add(sev, code, msg, opts...)
}

func (r *Result) Errors() []Violation { return r.filter(SeverityError) }
func (r *Result) Warnings() []Violation {
	return r.filter(SeverityWarn)
}

func (r *Result) filter(s Severity) []Violation {
	var out []Violation
	for _, v := range r.Violations {
		if v.Severity == s {
			out = append(out, v)
		}
	}
	return out
}

// OK reports whether the package may be stored. Warnings do not affect it.
func (r *Result) OK() bool { return len(r.Errors()) == 0 }

func (r *Result) Has(code string) bool {
	for _, v := range r.Violations {
		if v.Code == code {
			return true
		}
	}
	return false
}

// Merge folds another result in, preserving order.
func (r *Result) Merge(o *Result) {
	if o != nil {
		r.Violations = append(r.Violations, o.Violations...)
	}
}

// Sorted returns violations errors-first, then by path and line — the order a
// person wants to read them in.
func (r *Result) Sorted() []Violation {
	out := append([]Violation(nil), r.Violations...)
	rank := map[Severity]int{SeverityError: 0, SeverityWarn: 1, SeverityInfo: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func (r *Result) String() string {
	var b strings.Builder
	for _, v := range r.Sorted() {
		b.WriteString(v.String())
		b.WriteByte('\n')
	}
	return b.String()
}
