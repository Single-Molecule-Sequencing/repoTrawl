package output

// Verbosity controls how much output the reporter produces.
type Verbosity int

const (
	VerbositySummary  Verbosity = iota // default: summary table only
	VerboseStreaming                    // -v: streaming progress + summary
	VerboseTrace                       // -vv: full git output + summary
)

// Config holds rendering configuration for the reporter.
type Config struct {
	Verbosity Verbosity
	Color     bool   // true if stdout is a TTY
	OrgName   string
}
