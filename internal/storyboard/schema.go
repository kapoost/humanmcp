package storyboard

// Schema mirrors the YAML structure under storyboards/.
//
// One file = one Storyboard. The runner walks storyboards/ and dispatches
// each Storyboard based on Kind. See storyboards/README.md for the human
// description of each field; this is the machine view.

type Storyboard struct {
	ID                     string   `yaml:"id"`
	Version                string   `yaml:"version"`
	Title                  string   `yaml:"title"`
	Category               string   `yaml:"category"`
	Summary                string   `yaml:"summary"`
	Narrative              string   `yaml:"narrative"`
	Kind                   string   `yaml:"kind"` // http | unit | store
	ClosesFailureModes     []string `yaml:"closes_failure_modes"`
	KnownRegressionsCaught []string `yaml:"known_regressions_caught"`

	// kind: http
	Setup       SetupBlock       `yaml:"setup"`
	HTTPCases   []HTTPAssertion  `yaml:"assertions"`

	// kind: unit — overrides HTTPCases; the runner dispatches on Function name.
	Function  string      `yaml:"function"`
	UnitCases []UnitCase  `yaml:"cases"`

	// kind: store
	Operations      []StoreOp        `yaml:"operations"`
	StoreAssertions []StoreAssertion `yaml:"store_assertions"`
}

type SetupBlock struct {
	Listings []map[string]interface{} `yaml:"listings"`
	Pieces   []map[string]interface{} `yaml:"pieces"`
}

type HTTPAssertion struct {
	Name    string         `yaml:"name"`
	Request HTTPRequest    `yaml:"request"`
	Expect  HTTPExpect     `yaml:"expect"`
}

type HTTPRequest struct {
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Owner   bool              `yaml:"owner"`
	Body    string            `yaml:"body"`
	Form    map[string]string `yaml:"form"`
	Files   []FileUpload      `yaml:"files"`
	Headers map[string]string `yaml:"headers"`
}

// FileUpload describes a multipart file attachment for an HTTP request.
// When Files is non-empty the runner builds a multipart/form-data body
// merging Form fields and these attachments.
type FileUpload struct {
	Field       string `yaml:"field"`
	Filename    string `yaml:"filename"`
	ContentB64  string `yaml:"content_b64"`
	ContentType string `yaml:"content_type"`
}

type HTTPExpect struct {
	Status              int      `yaml:"status"`
	LocationContains    string   `yaml:"location_contains"`
	BodyContains        []string `yaml:"body_contains"`
	BodyDoesNotContain  []string `yaml:"body_does_not_contain"`
}

type UnitCase struct {
	Name   string      `yaml:"name"`
	Input  interface{} `yaml:"input"`
	Expect interface{} `yaml:"expect"`
}

type StoreOp struct {
	Save   map[string]interface{} `yaml:"save"`
	Reload bool                   `yaml:"reload"`
	Load   string                 `yaml:"load"`
}

type StoreAssertion struct {
	On         string `yaml:"on"` // slug of loaded piece
	Field      string `yaml:"field"`
	Equals     string `yaml:"equals"`
	StartsWith string `yaml:"starts_with"`
	NotEmpty   bool   `yaml:"not_empty"`
}
